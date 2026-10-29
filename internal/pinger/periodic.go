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
// it ends the watcher and waits for its goroutine to exit, so callers can
// defer it after the resource's own deferred Close and know no watcher
// outlives the call.
func closeOnCancel(ctx context.Context, fn func()) (stop func()) {
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			fn()
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}
