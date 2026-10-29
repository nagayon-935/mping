package pinger

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloseOnCancelRunsFnOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fired := make(chan struct{})
	stop := closeOnCancel(ctx, func() { close(fired) })

	cancel()
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("fn was not called after cancel")
	}
	stop() // joins the watcher goroutine
}

func TestCloseOnCancelStopWithoutCancelSkipsFn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	stop := closeOnCancel(ctx, func() { calls.Add(1) })

	stop()
	cancel() // cancelling after stop must not invoke fn

	if got := calls.Load(); got != 0 {
		t.Fatalf("fn calls = %d, want 0", got)
	}
}
