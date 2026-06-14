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
	dest := ts.IP
	if dest == "" {
		dest = ts.Host
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
			hopCount = discover(ctx, prober, sock, mtr, dest, cfg)
		case <-probeTicker.C:
			probe(ctx, prober, sock, mtr, dest, hopCount, cfg)
		}
	}
}

// discover sends TTL-limited probes from TTL=1 up to maxHops to find hop IPs.
// It updates mtr hop IPs and returns the final hop count (TTL at which dest was reached,
// or maxHops if not reached).
func discover(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, cfg Config) int {
	traceID := prober.NextTraceID()
	hopCount := cfg.MaxHops
	for ttl := 1; ttl <= cfg.MaxHops; ttl++ {
		if ctx.Err() != nil {
			return 0
		}
		reply, err := prober.ProbeHop(ctx, sock, dest, ttl, traceID, cfg.HopTimeout)
		if err != nil || ctx.Err() != nil {
			return 0
		}
		mtr.EnsureLen(ttl)
		if reply.Responded && reply.SrcIP != "" {
			info := prober.ASNInfoFor(reply.SrcIP)
			mtr.SetIP(ttl, reply.SrcIP, info.Number, info.Country, info.Org)
		}
		if reply.ReachedDest {
			hopCount = ttl
			break
		}
	}
	return hopCount
}

// probe sends one probe per hop for a single round and records results.
func probe(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, hopCount int, cfg Config) {
	traceID := prober.NextTraceID()
	for ttl := 1; ttl <= hopCount; ttl++ {
		if ctx.Err() != nil {
			return
		}
		reply, err := prober.ProbeHop(ctx, sock, dest, ttl, traceID, cfg.HopTimeout)
		if err != nil || ctx.Err() != nil {
			return
		}
		if reply.Responded {
			info := prober.ASNInfoFor(reply.SrcIP)
			mtr.RecordReply(ttl, reply.SrcIP, info.Number, info.Country, info.Org, reply.RTT)
		} else {
			mtr.RecordLoss(ttl)
		}
	}
}
