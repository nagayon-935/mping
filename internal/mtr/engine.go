// Package mtr discovers the route to each target and, unless configured for
// the path only, monitors per-hop loss and latency MTR-style.
package mtr

import (
	"context"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

const (
	defaultMaxHops         = 30
	unresponsiveMaxHops    = 20
	defaultProbeInterval   = 1 * time.Second
	defaultHopTimeout      = 1 * time.Second
	defaultRediscoverEvery = 10 * time.Minute
	// defaultMaxConcurrentProbes bounds the number of ProbeHop calls in
	// flight at once across the whole Engine (all targets combined). Without
	// this, discover()'s per-TTL fan-out (up to MaxHops goroutines) times a
	// large host count can burst to thousands of simultaneous goroutines and
	// heavy contention on the shared pinger's trace-channel registry.
	defaultMaxConcurrentProbes = 50
)

// HopSocket is the interface satisfied by *pinger.HopSocket.
// Abstracted here so the engine can be tested without real sockets.
type HopSocket interface {
	Close()
}

// HopProber is implemented by cmd/main's pingerMTRAdapter.
// It provides all primitives the engine needs to send probes and receive replies.
type HopProber interface {
	OpenHopSocket(ctx context.Context, dest string) (HopSocket, error)
	ProbeHop(ctx context.Context, sock HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error)
	// NextTraceID reserves an ID for one probe; ProbeHop takes ownership and
	// releases it on return. A negative ID signals exhaustion.
	NextTraceID() int
	// ASNInfoFor and PTRFor give up when ctx is cancelled (target stopped).
	ASNInfoFor(ctx context.Context, ip string) pinger.ASNInfo
	// PTRFor returns ip's reverse DNS name, or "" when unknown or disabled.
	PTRFor(ctx context.Context, ip string) string
}

// Config holds tunable parameters for the MTR engine.
type Config struct {
	// PathOnly limits the engine to discovering the route, at start and every
	// RediscoverEvery, without probing each hop every ProbeInterval.
	PathOnly bool
	// MaxHops defaults to 30, or 20 when the destination does not answer Echo.
	// An explicit limit applies regardless of destination reachability.
	MaxHops         int
	ProbeInterval   time.Duration
	HopTimeout      time.Duration
	RediscoverEvery time.Duration
	// MaxConcurrentProbes bounds the number of ProbeHop calls in flight at
	// once across the entire Engine (all targets combined, not per target —
	// a per-target limit would still allow NumTargets × MaxHops goroutines
	// at once). Defaults to defaultMaxConcurrentProbes when <= 0.
	MaxConcurrentProbes int
	// OnFlap is called (in the engine goroutine) when a route change is detected.
	// host is the target's display name (TargetStats.DisplayName); desc
	// summarises what changed.
	OnFlap func(host, desc string)
	// AnnotationRetry is how long an incomplete ASN/PTR result is kept before
	// it is looked up again. Defaults to defaultAnnotationRetry when <= 0.
	AnnotationRetry time.Duration

	limitUnresponsive bool
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.MaxHops <= 0 {
		out.MaxHops = defaultMaxHops
		out.limitUnresponsive = true
	}
	if out.ProbeInterval <= 0 {
		out.ProbeInterval = defaultProbeInterval
	}
	if out.HopTimeout <= 0 {
		out.HopTimeout = defaultHopTimeout
	}
	if out.RediscoverEvery <= 0 {
		out.RediscoverEvery = defaultRediscoverEvery
	}
	if out.MaxConcurrentProbes <= 0 {
		out.MaxConcurrentProbes = defaultMaxConcurrentProbes
	}
	if out.AnnotationRetry <= 0 {
		out.AnnotationRetry = defaultAnnotationRetry
	}
	return out
}

// Engine manages one MTR goroutine per target.
type Engine struct {
	prober  HopProber
	targets []*stats.TargetStats
	cfg     Config
	// sem bounds total concurrent ProbeHop calls across all targets; see
	// Config.MaxConcurrentProbes.
	sem chan struct{}

	mu         sync.Mutex
	targetRuns map[*stats.TargetStats]*targetRun
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewEngine creates an Engine. Call Start to begin probing.
func NewEngine(prober HopProber, targets []*stats.TargetStats, cfg Config) *Engine {
	resolved := cfg.withDefaults()
	return &Engine{
		prober:  prober,
		targets: targets,
		cfg:     resolved,
		sem:     make(chan struct{}, resolved.MaxConcurrentProbes),
	}
}

// Start launches one goroutine per target. Safe to call once.
func (e *Engine) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancel = cancel
	e.ctx = ctx
	e.targetRuns = make(map[*stats.TargetStats]*targetRun)
	e.mu.Unlock()
	for _, t := range e.targets {
		e.AddTarget(t)
	}
}

// Stop signals all goroutines to exit and waits for them to finish.
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	e.wg.Wait()
}

// runTarget is the per-target goroutine: discover the route, rediscover it
// every RediscoverEvery and, unless PathOnly, probe every hop each
// ProbeInterval. A failed socket open is recorded on the target's MTRStats
// and retried at the next rediscovery. Explicit unreachable replies pause
// per-hop monitoring until discovery succeeds again.
func runTarget(ctx context.Context, prober HopProber, ts *stats.TargetStats, cfg Config, sem chan struct{}) {
	mtr := ts.MTR()
	ann := newHopAnnotator(ctx, prober, mtr, cfg.AnnotationRetry)
	defer ann.close()
	var sock HopSocket
	var dest string
	defer func() {
		if sock != nil {
			sock.Close()
		}
	}()
	hopCount := 0
	waitingForRoute := false

	rediscover := func() {
		// Reopen the socket when the target's address changed after DNS
		// re-resolution.
		if current := ts.EffectiveAddress(); sock == nil || current != dest {
			next, err := prober.OpenHopSocket(ctx, current)
			if err != nil {
				if ctx.Err() == nil {
					mtr.SetError(err.Error())
				}
				return
			}
			if sock != nil {
				sock.Close()
			}
			sock, dest = next, current
		}
		prevIPs := mtr.RouteSnapshot()
		newHopCount, err := discover(ctx, prober, sock, mtr, dest, cfg, sem, ann)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			waitingForRoute = true
			mtr.SetError(err.Error())
			return
		}
		waitingForRoute = false
		mtr.SetError("")
		first := hopCount == 0
		hopCount = newHopCount
		if first {
			return
		}
		if changed, desc := mtr.CheckFlap(prevIPs, time.Now()); changed && cfg.OnFlap != nil {
			cfg.OnFlap(ts.DisplayName(), desc)
		}
	}
	rediscover()

	rediscoverTicker := time.NewTicker(cfg.RediscoverEvery)
	defer rediscoverTicker.Stop()
	var probeC <-chan time.Time
	if !cfg.PathOnly {
		probeTicker := time.NewTicker(cfg.ProbeInterval)
		defer probeTicker.Stop()
		probeC = probeTicker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-rediscoverTicker.C:
			rediscover()
		case <-probeC:
			if hopCount > 0 && !waitingForRoute {
				if err := probe(ctx, prober, sock, mtr, dest, hopCount, cfg, sem, ann); err != nil && ctx.Err() == nil {
					waitingForRoute = true
					mtr.SetError(err.Error())
				}
			}
		}
	}
}

// Each target has its own lifetime, while all share the engine's probe limit.
type targetRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// AddTarget starts monitoring t unless it is already registered.
// It panics if called before Start initializes the engine context.
func (e *Engine) AddTarget(t *stats.TargetStats) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.targetRuns[t]; exists {
		return
	}
	ctx, cancel := context.WithCancel(e.ctx)
	r := &targetRun{cancel: cancel, done: make(chan struct{})}
	e.targetRuns[t] = r
	e.wg.Go(func() { defer close(r.done); runTarget(ctx, e.prober, t, e.cfg, e.sem) })
}

// RemoveTarget cancels and joins t's monitor; an unregistered target is a no-op,
// including when called before Start.
func (e *Engine) RemoveTarget(t *stats.TargetStats) {
	e.mu.Lock()
	r := e.targetRuns[t]
	delete(e.targetRuns, t)
	e.mu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
	}
}
