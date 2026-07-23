// Package mtr implements MTR-style continuous per-hop loss/latency monitoring.
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

// HopProber is the interface satisfied by *pinger.Pinger (via cmd/main adapter).
// It provides all primitives the engine needs to send probes and receive replies.
type HopProber interface {
	OpenHopSocket(dest string) (HopSocket, error)
	ProbeHop(ctx context.Context, sock HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error)
	NextTraceID() int
	ASNInfoFor(ip string) pinger.ASNInfo
}

// Config holds tunable parameters for the MTR engine.
type Config struct {
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
	// host is TargetStats.Host; desc summarises what changed.
	OnFlap func(host, desc string)
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.MaxHops <= 0 {
		out.MaxHops = defaultMaxHops
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

	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	e.cancel = cancel
	for _, t := range e.targets {
		e.wg.Add(1)
		go func(ts *stats.TargetStats) {
			defer e.wg.Done()
			runTarget(ctx, e.prober, ts, e.cfg, e.sem)
		}(t)
	}
}

// Stop signals all goroutines to exit and waits for them to finish.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
}

// runTarget is the per-target goroutine: discover hops, then continuously probe.
func runTarget(ctx context.Context, prober HopProber, ts *stats.TargetStats, cfg Config, sem chan struct{}) {
	v := ts.GetView()
	dest := v.IP
	if dest == "" {
		dest = v.Host
	}

	sock, err := prober.OpenHopSocket(dest)
	if err != nil {
		return
	}
	defer sock.Close()

	mtr := ts.MTR()

	// Initial discovery
	hopCount := discover(ctx, prober, sock, mtr, dest, cfg, sem)
	if hopCount == 0 || ctx.Err() != nil {
		return
	}

	probeTicker := time.NewTicker(cfg.ProbeInterval)
	defer probeTicker.Stop()
	rediscoverTicker := time.NewTicker(cfg.RediscoverEvery)
	defer rediscoverTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rediscoverTicker.C:
			// Check if target IP has changed after DNS re-resolution and recreate socket
			v := ts.GetView()
			currentDest := v.IP
			if currentDest == "" {
				currentDest = v.Host
			}
			if currentDest != dest {
				sock.Close()
				newSock, err := prober.OpenHopSocket(currentDest)
				if err != nil {
					return
				}
				sock = newSock
				dest = currentDest
			}

			prevIPs := mtr.RouteSnapshot()
			newHopCount := discover(ctx, prober, sock, mtr, dest, cfg, sem)
			if newHopCount == 0 || ctx.Err() != nil {
				// Round was cancelled mid-flight (e.g. Stop() racing this
				// tick); discover() registered nothing this round, so
				// comparing against prevIPs would report a bogus flap.
				// Keep the previous hopCount and let the next loop
				// iteration observe ctx.Done().
				continue
			}
			hopCount = newHopCount
			if changed, desc := mtr.CheckFlap(prevIPs, time.Now()); changed && cfg.OnFlap != nil {
				cfg.OnFlap(ts.Host, desc)
			}
		case <-probeTicker.C:
			probe(ctx, prober, sock, mtr, dest, hopCount, cfg, sem)
		}
	}
}

// discover sends TTL-limited probes from TTL=1 up to maxHops concurrently to find hop IPs.
// It updates mtr hop IPs and returns the final hop count (TTL at which dest was reached,
// or maxHops if not reached). sem bounds concurrent ProbeHop calls across the
// whole Engine; a nil sem (only in tests bypassing NewEngine) disables the
// bound.
func discover(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, cfg Config, sem chan struct{}) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	reached := false
	firstReachedTTL := cfg.MaxHops + 1

	type result struct {
		ttl   int
		reply pinger.HopReply
	}
	results := make([]result, 0, cfg.MaxHops)

	for ttl := 1; ttl <= cfg.MaxHops; ttl++ {
		wg.Add(1)
		go func(t int) {
			defer wg.Done()
			if !acquire(ctx, sem) {
				return
			}
			defer release(sem)

			traceID := prober.NextTraceID()
			reply, err := prober.ProbeHop(ctx, sock, dest, t, traceID, cfg.HopTimeout)
			if err != nil || ctx.Err() != nil {
				return
			}
			mu.Lock()
			results = append(results, result{ttl: t, reply: reply})
			if reply.ReachedDest {
				reached = true
				if t < firstReachedTTL {
					firstReachedTTL = t
				}
			}
			mu.Unlock()
		}(ttl)
	}
	wg.Wait()

	// If cancellation happened mid-round, per-TTL goroutines may have
	// observed ctx.Err() at different times: some appended their result
	// before cancellation, others bailed out after. Registering that torn
	// result set would corrupt mtr's hop count / route (e.g. a hop that
	// actually reached the destination missing because its goroutine ran
	// after cancellation, while unresponsive higher-TTL hops still got in).
	// Discard the whole round instead; 0 is never returned otherwise since
	// hopCount is always >= 1 below.
	if ctx.Err() != nil {
		return 0
	}

	hopCount := cfg.MaxHops
	if reached {
		hopCount = firstReachedTTL
	}

	// Drop any trailing hops from a previously longer path so the UI doesn't
	// keep rendering frozen IP/RTT data for hops that no longer exist.
	mtr.TruncateLen(hopCount)

	// Register hops only up to the discovered hopCount to avoid leaking outer hops
	for _, res := range results {
		if res.ttl <= hopCount {
			mtr.EnsureLen(res.ttl)
			if res.reply.Responded && res.reply.SrcIP != "" {
				info := prober.ASNInfoFor(res.reply.SrcIP)
				mtr.SetIP(res.ttl, res.reply.SrcIP, info.Number, info.Country, info.Org)
			}
		}
	}

	return hopCount
}

// probe sends one probe per hop concurrently for a single round and records
// results. Hops that have never had a responder IP recorded (rendered as
// "*" in the UI) are skipped: continuous per-tick probing wouldn't reveal
// anything discover()/rediscovery hasn't already tried, and would just log
// permanent 100% loss for a hop the user never learned the address of. The
// row stays visible via the hop slot discover() already created. sem bounds
// concurrent ProbeHop calls across the whole Engine; see discover.
func probe(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, hopCount int, cfg Config, sem chan struct{}) {
	var wg sync.WaitGroup
	for ttl := 1; ttl <= hopCount; ttl++ {
		if !mtr.HasIP(ttl) {
			continue
		}
		wg.Add(1)
		go func(t int) {
			defer wg.Done()
			if !acquire(ctx, sem) {
				return
			}
			defer release(sem)

			traceID := prober.NextTraceID()
			reply, err := prober.ProbeHop(ctx, sock, dest, t, traceID, cfg.HopTimeout)
			if err != nil || ctx.Err() != nil {
				return
			}
			if reply.Responded {
				info := prober.ASNInfoFor(reply.SrcIP)
				mtr.RecordReply(t, reply.SrcIP, info.Number, info.Country, info.Org, reply.RTT)
			} else {
				mtr.RecordLoss(t)
			}
		}(ttl)
	}
	wg.Wait()
}

// acquire blocks until it obtains a slot in sem or ctx is cancelled,
// whichever comes first, returning false in the latter case (the caller
// should then return without calling release). A nil sem disables the
// bound entirely (acquire always succeeds immediately) — used only by
// tests that construct discover/probe calls without going through
// NewEngine/Start.
func acquire(ctx context.Context, sem chan struct{}) bool {
	if sem == nil {
		return true
	}
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// release returns the slot acquire took. No-op when sem is nil, mirroring
// acquire's bypass.
func release(sem chan struct{}) {
	if sem == nil {
		return
	}
	<-sem
}
