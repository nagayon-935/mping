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
	return out
}

// Engine manages one MTR goroutine per target.
type Engine struct {
	prober  HopProber
	targets []*stats.TargetStats
	cfg     Config

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine creates an Engine. Call Start to begin probing.
func NewEngine(prober HopProber, targets []*stats.TargetStats, cfg Config) *Engine {
	return &Engine{
		prober:  prober,
		targets: targets,
		cfg:     cfg.withDefaults(),
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
			runTarget(ctx, e.prober, ts, e.cfg)
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
func runTarget(ctx context.Context, prober HopProber, ts *stats.TargetStats, cfg Config) {
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
	hopCount := discover(ctx, prober, sock, mtr, dest, cfg)
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
			newHopCount := discover(ctx, prober, sock, mtr, dest, cfg)
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
			probe(ctx, prober, sock, mtr, dest, hopCount, cfg)
		}
	}
}

// discover sends TTL-limited probes from TTL=1 up to maxHops concurrently to find hop IPs.
// It updates mtr hop IPs and returns the final hop count (TTL at which dest was reached,
// or maxHops if not reached).
func discover(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, cfg Config) int {
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

// probe sends one probe per hop concurrently for a single round and records results.
func probe(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, hopCount int, cfg Config) {
	var wg sync.WaitGroup
	for ttl := 1; ttl <= hopCount; ttl++ {
		wg.Add(1)
		go func(t int) {
			defer wg.Done()
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
