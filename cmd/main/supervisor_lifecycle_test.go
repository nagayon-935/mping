package main

import (
	"sync"
	"testing"
)

// TestSupervisorSerializesConcurrentRestarts verifies that concurrent restarts
// queue on the command loop (supervisor_loop.go) instead of racing: exactly one
// pinger is live and every earlier one was released. (The old
// TestStartPingerReleasesSupersededPinger asserted that the loser of two
// concurrent startPinger() calls was cleaned up; the command queue now makes
// that race impossible, and TestHandle_RestartReleasesPreviousPinger covers
// the sequential release path.)
func TestSupervisorSerializesConcurrentRestarts(t *testing.T) {
	sup, created := newStateTestSupervisor(t)
	sup.Start()
	defer sup.Shutdown()

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := sup.do(cmdRestart); err != nil {
				t.Errorf("cmdRestart: %v", err)
			}
		})
	}
	wg.Wait()

	live := sup.pingerSnap.Load()
	if live == nil {
		t.Fatal("no live pinger after restarts")
	}

	liveCount := 0
	for i, fp := range *created {
		if pingerController(fp) == *live {
			liveCount++
			continue
		}
		if !fp.isReleased() {
			t.Errorf("superseded pinger #%d leaked (stop and close were not both called)", i)
		}
	}
	if liveCount != 1 {
		t.Errorf("expected exactly 1 live pinger, got %d of %d", liveCount, len(*created))
	}
}
