package stats

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildSnapshot_Empty(t *testing.T) {
	snap := BuildSnapshot(nil, nil)
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

	snap := BuildSnapshot([]*TargetStats{ts}, nil)
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

// TestBuildSnapshot_DuplicatesAndLateReplies pins that both counters flow
// through BuildSnapshot into the exported JSON fields.
func TestBuildSnapshot_DuplicatesAndLateReplies(t *testing.T) {
	ts := NewTargetStats("example.com")
	ts.SetIP("1.2.3.4")
	ts.IncSent()
	ts.OnSuccess(10*time.Millisecond, 64)
	ts.OnDuplicate()
	ts.OnDuplicate()
	ts.OnLateReply()

	snap := BuildSnapshot([]*TargetStats{ts}, nil)
	st := snap.Targets[0]
	if st.Duplicates != 2 {
		t.Errorf("duplicates = %d, want 2", st.Duplicates)
	}
	if st.LateReplies != 1 {
		t.Errorf("late_replies = %d, want 1", st.LateReplies)
	}
	// Recv must not be inflated by the duplicates recorded above.
	if st.Recv != 1 {
		t.Errorf("recv = %d, want 1 (duplicates must not inflate Recv)", st.Recv)
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	targets := out["targets"].([]any)
	target0 := targets[0].(map[string]any)
	if target0["duplicates"] != float64(2) {
		t.Errorf("json duplicates = %v, want 2", target0["duplicates"])
	}
	if target0["late_replies"] != float64(1) {
		t.Errorf("json late_replies = %v, want 1", target0["late_replies"])
	}
}

func TestBuildSnapshot_JSON(t *testing.T) {
	ts := NewTargetStats("google.com")
	ts.SetIP("8.8.8.8")
	ts.IncSent()
	ts.OnSuccess(8*time.Millisecond, 115)

	snap := BuildSnapshot([]*TargetStats{ts}, nil)
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
	snap := BuildSnapshot([]*TargetStats{ts}, nil)
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

	snap := BuildSnapshot([]*TargetStats{ts}, nil)
	st := snap.Targets[0]
	if len(st.TraceHops) != 2 {
		t.Errorf("expected 2 trace hops, got %d", len(st.TraceHops))
	}
}

func TestBuildSnapshot_PMTU(t *testing.T) {
	ts := NewTargetStats("host.local")
	ts.SetPMTU(1400)
	ts.SetPMTUBottleneckIP("10.0.0.1")

	snap := BuildSnapshot([]*TargetStats{ts}, nil)
	st := snap.Targets[0]
	if st.PMTU != 1400 {
		t.Errorf("expected PMTU=1400, got %d", st.PMTU)
	}
	if st.PMTUBottleneckIP != "10.0.0.1" {
		t.Errorf("expected PMTUBottleneckIP=10.0.0.1, got %q", st.PMTUBottleneckIP)
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

func TestBuildSnapshot_HTTPChecksSummarizeEachResult(t *testing.T) {
	up := NewHTTPCheckResult("https://up.example/health")
	up.SetResult(200, 15*time.Millisecond, nil)
	down := NewHTTPCheckResult("https://down.example/health")
	down.SetResult(503, 30*time.Millisecond, nil)

	snap := BuildSnapshot(nil, []*HTTPCheckResult{up, down})

	if len(snap.HTTPChecks) != 2 {
		t.Fatalf("HTTPChecks = %d, want 2", len(snap.HTTPChecks))
	}
	got := snap.HTTPChecks[0]
	if got.URL != "https://up.example/health" || got.Status != "Up" || got.StatusCode != 200 {
		t.Errorf("first check = %+v, want Up 200 for up.example", got)
	}
	if got.LastRTTMs != 15 || got.UpCount != 1 || got.DownCount != 0 {
		t.Errorf("first check RTT/counts = %v/%d/%d, want 15/1/0", got.LastRTTMs, got.UpCount, got.DownCount)
	}
	if snap.HTTPChecks[1].Status != "Down" || snap.HTTPChecks[1].DownCount != 1 {
		t.Errorf("second check = %+v, want Down with one failure", snap.HTTPChecks[1])
	}
}

func TestBuildSnapshotFromViews_PortsAndMTRHopsUseMilliseconds(t *testing.T) {
	view := TargetView{
		Host: "router.example",
		PortResults: []PortCheckView{
			{Port: 443, Protocol: "tcp", Status: "Open", RTT: 2500 * time.Microsecond, OpenCount: 3, ClosedCount: 1},
		},
		MTRHops: []HopView{
			{TTL: 1, IP: "192.0.2.1", ASN: "AS64500", Country: "JP", Org: "Example", Sent: 10, Recv: 9, LossPct: 10,
				LastRTT: time.Millisecond, AvgRTT: 2 * time.Millisecond, MinRTT: 500 * time.Microsecond,
				MaxRTT: 4 * time.Millisecond, Jitter: 250 * time.Microsecond},
		},
	}

	snap := BuildSnapshotFromViews([]TargetView{view}, nil)

	target := snap.Targets[0]
	wantPort := PortSummary{Port: 443, Protocol: "tcp", Status: "Open", RTTMs: 2.5, OpenCount: 3, ClosedCount: 1}
	if len(target.PortResults) != 1 || target.PortResults[0] != wantPort {
		t.Errorf("PortResults = %+v, want [%+v]", target.PortResults, wantPort)
	}
	wantHop := HopSummary{TTL: 1, IP: "192.0.2.1", ASN: "AS64500", Country: "JP", Org: "Example", Sent: 10, Recv: 9,
		LossPct: 10, LastRTTMs: 1, AvgRTTMs: 2, MinRTTMs: 0.5, MaxRTTMs: 4, JitterMs: 0.25}
	if len(target.MTRHops) != 1 || target.MTRHops[0] != wantHop {
		t.Errorf("MTRHops = %+v, want [%+v]", target.MTRHops, wantHop)
	}
	if snap.HTTPChecks != nil {
		t.Errorf("HTTPChecks = %+v, want nil without HTTP results", snap.HTTPChecks)
	}
}
