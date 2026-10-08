package main

import (
	"context"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// tracer is the part of the pinger runTraceroutes needs.
type tracer interface {
	TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error)
}

// runTraceroutes traces every target in parallel right away and then every
// tracerouteInterval until ctx is cancelled.
func runTraceroutes(ctx context.Context, p tracer, targets []*stats.TargetStats) {
	ticker := time.NewTicker(tracerouteInterval)
	defer ticker.Stop()

	runOnce := func() {
		if ctx.Err() != nil {
			return
		}
		for _, t := range targets {
			if len(t.GetView().TraceHops) == 0 {
				t.SetTraceHops([]string{"Tracing..."})
			}
		}

		var wg sync.WaitGroup
		for _, t := range targets {
			wg.Add(1)
			go func(t *stats.TargetStats) {
				defer wg.Done()
				var hops []string
				var err error
				hops, err = p.TraceRoute(ctx, t.Host, tracerouteMaxHops, tracerouteHopTimeout)
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
			}(t)
		}
		wg.Wait()
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
