package stats

import (
	"testing"
	"time"
)

func TestResetClearsJitterHistory(t *testing.T) {
	target := NewTargetStats("localhost")
	target.OnSuccess(time.Millisecond, 64)
	target.OnSuccess(time.Second, 64)
	if target.GetView().Jitter == 0 {
		t.Fatal("fixture needs nonzero jitter")
	}
	target.Reset()
	if got := target.GetView().Jitter; got != 0 {
		t.Fatalf("jitter after Reset=%v", got)
	}
	for range 2 {
		target.OnSuccess(5*time.Millisecond, 64)
	}
	if got := target.GetView().Jitter; got != 0 {
		t.Fatalf("old jitter carried into new samples: %v", got)
	}
}
