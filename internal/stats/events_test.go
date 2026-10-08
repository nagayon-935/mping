package stats

import (
	"testing"
	"time"
)

func TestTargetEventsAreBoundedAndIndependentOfView(t *testing.T) {
	target := NewTargetStats("same")
	for i := 0; i < maxTargetEvents+5; i++ {
		target.RecordEvent("test", "event")
	}
	events, dropped := target.Events()
	if len(events) != maxTargetEvents || dropped != 5 {
		t.Fatalf("events=%d dropped=%d", len(events), dropped)
	}
	for _, event := range events {
		if event.TargetID != target.ID {
			t.Fatal("identity missing")
		}
	}
	events[0].Message = "changed"
	copy, _ := target.Events()
	if copy[0].Message == "changed" {
		t.Fatal("mutable event snapshot")
	}
	target.OnFailure("Timeout")
	target.OnSuccess(time.Millisecond, 64)
	target.Reset()
	last, _ := target.Events()
	if last[len(last)-1].Kind != "reset" || last[len(last)-2].Kind != "recovery" {
		t.Fatal("measurement events missing")
	}
}
