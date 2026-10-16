package stats

import (
	"strings"
	"testing"
	"time"
)

func TestHealthDetectsFailureAfterLongHealthyRunAndRequiresRecovery(t *testing.T) {
	target := NewTargetStats("router")
	target.ConfigureHealth(DefaultHealthPolicy())
	for range 10000 {
		target.OnSuccess(time.Millisecond, 64)
	}
	for range 3 {
		target.OnFailure("timeout")
	}
	v := target.GetView()
	if !v.Health.Unavailable || v.Health.Status != "crit" || v.Loss != 3 || v.Recv != 10000 {
		t.Fatalf("failure hidden by lifetime counters: %+v", v.Health)
	}
	target.OnSuccess(time.Millisecond, 64)
	if !target.GetView().Health.Unavailable {
		t.Fatal("recovered after only one reply")
	}
	target.OnSuccess(time.Millisecond, 64)
	if h := target.GetView().Health; h.Unavailable || h.Status != "ok" {
		t.Fatalf("did not recover: %+v", h)
	}
	events, _ := target.Events()
	failures := 0
	for _, e := range events {
		if e.Kind == "ping" {
			failures++
			if e.Count != 3 {
				t.Fatalf("count=%d", e.Count)
			}
		}
	}
	if failures != 1 {
		t.Fatalf("failure events=%d", failures)
	}
}

func TestHealthInitialFailureDebounceAndRecentWindow(t *testing.T) {
	target := NewTargetStats("router")
	target.ConfigureHealth(DefaultHealthPolicy())
	for range 2 {
		target.OnFailure("timeout")
		if target.GetView().Health.Status != "warn" {
			t.Fatal("declared down before failure threshold")
		}
	}
	target.OnFailure("timeout")
	if !target.GetView().Health.Unavailable {
		t.Fatal("missing down transition")
	}
	now := time.Now()
	target.mu.Lock()
	target.health = targetHealth{policy: DefaultHealthPolicy(), configured: true}
	target.recordOutcomeLocked(now.Add(-2*time.Minute), false)
	target.recordOutcomeLocked(now, true)
	target.mu.Unlock()
	h := target.GetView().Health
	if h.RecentLoss != 0 || h.RecentCompleted != 1 || h.RecentLossPct != 0 {
		t.Fatalf("expired failures retained: %+v", h)
	}
}

func TestTimestampedHistoryRingResetAndMeasurementState(t *testing.T) {
	target := NewTargetStats("router")
	start := time.Now().Add(-time.Hour)
	target.mu.Lock()
	for i := range historySize + 10 {
		target.appendHistoryLocked(time.Millisecond, start.Add(time.Duration(i)*time.Second))
	}
	target.mu.Unlock()
	samples := target.HistorySamples(historySize)
	if len(samples) != historySize || !samples[0].At.Equal(start.Add(10*time.Second)) || *samples[len(samples)-1].RTTMS != 1 {
		t.Fatal("timestamp ring not aligned")
	}
	target.OnFailure("timeout")
	if target.HistorySamples(1)[0].RTTMS != nil {
		t.Fatal("loss is not null")
	}
	target.SetMeasurementState("stopped")
	target.Reset()
	if len(target.HistorySamples(10)) != 0 || target.GetView().MeasurementState != "stopped" {
		t.Fatal("reset resumed measurements or retained history")
	}
}

func TestHealthEventsShareBackendDecisionAndDoNotExposeStaleNames(t *testing.T) {
	target := NewTargetStats("router")
	target.ConfigureHealth(DefaultHealthPolicy())
	var events []Event
	target.SetEventSink(func(e Event) { events = append(events, e) })
	target.OnSuccess(time.Second, 64)
	if len(events) == 0 || events[len(events)-1].Kind != "health" || !strings.Contains(events[len(events)-1].Message, "RTT") {
		t.Fatalf("events=%v", events)
	}
	target.SetResolveAddress("192.0.2.1")
	target.SetDisplayName("edge")
	if target.GetView().Host != "edge" || target.ResolveAddress() != "192.0.2.1" {
		t.Fatal("rename changed probe address")
	}
}
