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

// closeOnCancel runs fn (typically closing a socket to unblock a pending
// read) once ctx is cancelled. The returned stop must be called exactly once:
// it unregisters the callback or, if cancellation already triggered fn, waits
// for fn to finish so no callback outlives the call.
func closeOnCancel(ctx context.Context, fn func()) (stop func()) {
	done := make(chan struct{})
	stopAfter := context.AfterFunc(ctx, func() {
		defer close(done)
		fn()
	})
	return func() {
		if !stopAfter() {
			<-done
		}
	}
}
