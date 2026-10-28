package pinger

import (
	"context"
	"time"
)

// runPeriodic performs the initial check before starting the interval timer.
// Checks run serially; each check owns its cancellation handling so shutdown
// never becomes a failed measurement.
func runPeriodic(ctx context.Context, interval time.Duration, check func()) {
	check()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
