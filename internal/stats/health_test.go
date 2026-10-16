package stats

import (
	"math"
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

func TestHealthPolicyValidateRejectsOutOfRangeSettings(t *testing.T) {
	valid := DefaultHealthPolicy()
	tests := []struct {
		name   string
		mutate func(*HealthPolicy)
		want   string
	}{
		{"no failures", func(p *HealthPolicy) { p.Failures = 0 }, "failure/recovery"},
		{"too many recoveries", func(p *HealthPolicy) { p.Recoveries = 10001 }, "failure/recovery"},
		{"short window", func(p *HealthPolicy) { p.Window = time.Millisecond }, "window"},
		{"long window", func(p *HealthPolicy) { p.Window = 2 * time.Hour }, "window"},
		{"rtt crit below warn", func(p *HealthPolicy) { p.RTTCrit = p.RTTWarn }, "thresholds"},
		{"loss above 100", func(p *HealthPolicy) { p.LossCrit = 101 }, "thresholds"},
		{"loss NaN", func(p *HealthPolicy) { p.LossWarn = math.NaN() }, "thresholds"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("default policy: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := DefaultHealthPolicy()
			tt.mutate(&p)

			err := p.Validate()

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want an error about %s", err, tt.want)
			}
		})
	}
}

func TestConfigureHealthFallsBackToDefaultsForAnInvalidPolicy(t *testing.T) {
	target := NewTargetStats("example.com")
	invalid := DefaultHealthPolicy()
	invalid.Window = 0

	target.ConfigureHealth(invalid)

	if got := target.health.policy; got != DefaultHealthPolicy() {
		t.Fatalf("policy = %+v, want the defaults", got)
	}
}

func TestMeasurementStateReturnsTheLastSetState(t *testing.T) {
	target := NewTargetStats("example.com")

	target.SetMeasurementState("stopped")

	if got := target.MeasurementState(); got != "stopped" {
		t.Fatalf("MeasurementState() = %q, want stopped", got)
	}
}

func TestHistorySamplesWithNegativeCountIsEmpty(t *testing.T) {
	target := NewTargetStats("example.com")
	target.IncSent()
	target.OnSuccess(time.Millisecond, 64)

	if got := target.HistorySamples(-1); len(got) != 0 {
		t.Fatalf("HistorySamples(-1) = %v, want none", got)
	}
}
