package stats

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildSnapshot_Empty(t *testing.T) {
	snap := BuildSnapshot(nil)
	if snap.Targets == nil {
		t.Error("Targets should not be nil")
	}
	if len(snap.Targets) != 0 {
		t.Errorf("expected 0 targets, got %d", len(snap.Targets))
	}
	if snap.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestBuildSnapshot_Basic(t *testing.T) {
	ts := NewTargetStats("example.com")
	ts.SetIP("1.2.3.4")
	ts.IncSent()
	ts.OnSuccess(10*time.Millisecond, 64)
	ts.IncSent()
	ts.OnSuccess(20*time.Millisecond, 64)
	ts.IncSent()
	ts.OnFailure("timeout")

	snap := BuildSnapshot([]*TargetStats{ts})
	if len(snap.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(snap.Targets))
	}
	st := snap.Targets[0]
	if st.Host != "example.com" {
		t.Errorf("host = %q, want %q", st.Host, "example.com")
	}
	if st.IP != "1.2.3.4" {
		t.Errorf("ip = %q, want %q", st.IP, "1.2.3.4")
	}
	if st.Sent != 3 {
		t.Errorf("sent = %d, want 3", st.Sent)
	}
	if st.Recv != 2 {
		t.Errorf("recv = %d, want 2", st.Recv)
	}
	if st.Loss != 1 {
		t.Errorf("loss = %d, want 1", st.Loss)
	}
	wantLoss := float64(1) / float64(3) * 100
	if st.LossRatePct != wantLoss {
		t.Errorf("loss_rate_pct = %f, want %f", st.LossRatePct, wantLoss)
	}
	if st.MinRTTMs != 10.0 {
		t.Errorf("min_rtt_ms = %f, want 10.0", st.MinRTTMs)
	}
	if st.MaxRTTMs != 20.0 {
		t.Errorf("max_rtt_ms = %f, want 20.0", st.MaxRTTMs)
	}
	if st.AvgRTTMs != 15.0 {
		t.Errorf("avg_rtt_ms = %f, want 15.0", st.AvgRTTMs)
	}
	if st.LastError != "timeout" {
		t.Errorf("last_error = %q, want %q", st.LastError, "timeout")
	}
	if st.LastLossTime == nil {
		t.Error("last_loss_time should not be nil after failure")
	}
}

func TestBuildSnapshot_JSON(t *testing.T) {
	ts := NewTargetStats("google.com")
	ts.SetIP("8.8.8.8")
	ts.IncSent()
	ts.OnSuccess(8*time.Millisecond, 115)

	snap := BuildSnapshot([]*TargetStats{ts})
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := out["timestamp"]; !ok {
		t.Error("missing 'timestamp' key in JSON")
	}
	targets, ok := out["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("expected targets array with 1 element, got %v", out["targets"])
	}
}

func TestBuildSnapshot_NoLoss(t *testing.T) {
	ts := NewTargetStats("host.local")
	snap := BuildSnapshot([]*TargetStats{ts})
	st := snap.Targets[0]
	if st.LossRatePct != 0.0 {
		t.Errorf("LossRatePct with Sent=0 should be 0.0, got %f", st.LossRatePct)
	}
	if st.LastLossTime != nil {
		t.Error("LastLossTime should be nil when no loss occurred")
	}
}

func TestBuildSnapshot_TraceHops(t *testing.T) {
	ts := NewTargetStats("host.local")
	ts.SetTraceHops([]string{"1.1.1.1", "2.2.2.2"})

	snap := BuildSnapshot([]*TargetStats{ts})
	st := snap.Targets[0]
	if len(st.TraceHops) != 2 {
		t.Errorf("expected 2 trace hops, got %d", len(st.TraceHops))
	}
}

func TestBuildSnapshot_PMTU(t *testing.T) {
	ts := NewTargetStats("host.local")
	ts.SetPMTU(1400)

	snap := BuildSnapshot([]*TargetStats{ts})
	st := snap.Targets[0]
	if st.PMTU != 1400 {
		t.Errorf("expected PMTU=1400, got %d", st.PMTU)
	}
}

func TestDurationMs(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want float64
	}{
		{0, 0},
		{time.Millisecond, 1.0},
		{10 * time.Millisecond, 10.0},
		{500 * time.Microsecond, 0.5},
	}
	for _, tt := range tests {
		if got := durationMs(tt.d); got != tt.want {
			t.Errorf("durationMs(%v) = %f, want %f", tt.d, got, tt.want)
		}
	}
}
