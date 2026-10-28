package main

import (
	"context"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// tracer is the part of the pinger runTraceroutes needs.
type tracer interface {
	TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error)
}

// runTraceroutes traces t right away and then every tracerouteInterval until
// ctx is cancelled.
func runTraceroutes(ctx context.Context, p tracer, t *stats.TargetStats) {
	ticker := time.NewTicker(tracerouteInterval)
	defer ticker.Stop()

	runOnce := func() {
		if ctx.Err() != nil {
			return
		}
		if len(t.GetView().TraceHops) == 0 {
			t.SetTraceHops([]string{"Tracing..."})
		}
		// Trace the monitored address, as MTR does. Resolving the host
		// independently can choose another address for multi-record DNS.
		hops, err := p.TraceRoute(ctx, t.EffectiveAddress(), tracerouteMaxHops, tracerouteHopTimeout)
		if ctx.Err() != nil {
			return // a cancelled run must not replace the displayed route
		}
		if err != nil {
			t.SetTraceHops([]string{"error: " + err.Error()})
			return
		}
		if len(hops) == 0 {
			t.SetTraceHops([]string{"no route found"})
			return
		}
		t.SetTraceHops(hops)
	}

	runOnce() // Initial run

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}
