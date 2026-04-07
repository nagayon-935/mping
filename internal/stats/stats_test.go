package stats

import (
	"testing"
	"time"
)

func TestTargetStatsSuccessStatsAndView(t *testing.T) {
	tgt := NewTargetStats("example.com")

	// Simulate 3 successful pings.
	rtts := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
	}
	for _, rtt := range rtts {
		tgt.IncSent()
		tgt.OnSuccess(rtt, 64)
	}

	view := tgt.GetView()
	if view.Sent != 3 {
		t.Fatalf("Sent: got %d, want 3", view.Sent)
	}
	if view.Recv != 3 {
		t.Fatalf("Recv: got %d, want 3", view.Recv)
	}
	if view.LastRTT != 30*time.Millisecond {
		t.Fatalf("LastRTT: got %v, want %v", view.LastRTT, 30*time.Millisecond)
	}
	if view.MinRTT != 10*time.Millisecond {
		t.Fatalf("MinRTT: got %v, want %v", view.MinRTT, 10*time.Millisecond)
	}
	if view.MaxRTT != 30*time.Millisecond {
		t.Fatalf("MaxRTT: got %v, want %v", view.MaxRTT, 30*time.Millisecond)
	}
	if view.AvgRTT != 20*time.Millisecond {
		t.Fatalf("AvgRTT: got %v, want %v", view.AvgRTT, 20*time.Millisecond)
	}

	// Jitter should be calculated using RFC 1889 (smoothed).
	// Sequence: 10ms, 20ms, 30ms
	// 1. RTT=10ms: Recv=0 (pre-inc), J=0.
	// 2. RTT=20ms: Recv=1. D=|20-10|=10. J = 0 + (10-0)/16 = 0.625ms.
	// 3. RTT=30ms: Recv=2. D=|30-20|=10. J = 0.625 + (10-0.625)/16 = 1.2109375ms.
	expJitter := 1210937 * time.Nanosecond // approx 1.210937ms
	if diff := view.Jitter - expJitter; diff < 0 {
		diff = -diff
		if diff > 100*time.Nanosecond {
			t.Fatalf("Jitter: got %v, want %v (diff %v)", view.Jitter, expJitter, diff)
		}
	} else if diff > 100*time.Nanosecond {
		t.Fatalf("Jitter: got %v, want %v (diff %v)", view.Jitter, expJitter, diff)
	}
}

func TestTargetStatsHistoryRing(t *testing.T) {
	tgt := NewTargetStats("example.com")
	for i := 1; i <= historySize+1; i++ {
		tgt.IncSent()
		tgt.OnSuccess(time.Duration(i)*time.Millisecond, 64)
	}

	view := tgt.GetView()
	if len(view.History) != historySize {
		t.Fatalf("History size: got %d, want %d", len(view.History), historySize)
	}
	if view.History[0] != 2*time.Millisecond {
		t.Fatalf("History[0]: got %v, want %v", view.History[0], 2*time.Millisecond)
	}
	if view.History[len(view.History)-1] != time.Duration(historySize+1)*time.Millisecond {
		t.Fatalf("History[last]: got %v, want %v", view.History[len(view.History)-1], time.Duration(historySize+1)*time.Millisecond)
	}
}

func TestPortCheckResult_SetAndGetResult(t *testing.T) {
	r := &PortCheckResult{Port: 443, Protocol: "tcp", Status: "Checking..."}

	r.SetResult("Open", 10*time.Millisecond)
	status, rtt, openCount, closedCount, _ := r.GetResult()
	if status != "Open" {
		t.Errorf("Status: got %q, want %q", status, "Open")
	}
	if rtt != 10*time.Millisecond {
		t.Errorf("RTT: got %v, want %v", rtt, 10*time.Millisecond)
	}
	if openCount != 1 {
		t.Errorf("OpenCount: got %d, want 1", openCount)
	}
	if closedCount != 0 {
		t.Errorf("ClosedCount: got %d, want 0", closedCount)
	}

	r.SetResult("Closed", 5*time.Millisecond)
	status, _, _, closedCount, _ = r.GetResult()
	if status != "Closed" {
		t.Errorf("Status: got %q, want %q", status, "Closed")
	}
	if closedCount != 1 {
		t.Errorf("ClosedCount: got %d, want 1", closedCount)
	}
}

func TestPortCheckResult_SetResult_LastChange(t *testing.T) {
	r := &PortCheckResult{Port: 80, Protocol: "tcp", Status: "Open"}

	// Same status: LastChange should not be updated
	before := r.LastChange
	r.SetResult("Open", 5*time.Millisecond)
	_, _, _, _, lastChange := r.GetResult()
	if lastChange != before {
		t.Error("LastChange should not update when status is unchanged")
	}

	// Different status: LastChange should be updated
	r.SetResult("Closed", 5*time.Millisecond)
	_, _, _, _, lastChange = r.GetResult()
	if lastChange.IsZero() {
		t.Error("LastChange should be set when status changes")
	}
}

func TestPortCheckResult_SetResult_AllStatuses(t *testing.T) {
	tests := []struct {
		status          string
		expectOpenInc   bool
		expectClosedInc bool
	}{
		{"Open", true, false},
		{"Closed", false, true},
		{"Filtered", false, true},
		{"Open|Filtered", false, true},
		{"Error", false, false},
	}
	for _, tt := range tests {
		r := &PortCheckResult{Status: "Checking..."}
		r.SetResult(tt.status, 0)
		_, _, open, closed, _ := r.GetResult()
		if tt.expectOpenInc && open != 1 {
			t.Errorf("status %q: OpenCount got %d, want 1", tt.status, open)
		}
		if tt.expectClosedInc && closed != 1 {
			t.Errorf("status %q: ClosedCount got %d, want 1", tt.status, closed)
		}
	}
}

func TestTargetStats_SetIP(t *testing.T) {
	tgt := NewTargetStats("example.com")
	tgt.SetIP("1.2.3.4")
	if tgt.GetView().IP != "1.2.3.4" {
		t.Errorf("SetIP: got %q, want %q", tgt.GetView().IP, "1.2.3.4")
	}
}

func TestTargetStats_SetTraceHops(t *testing.T) {
	tgt := NewTargetStats("example.com")
	hops := []string{"10.0.0.1", "10.0.0.2", "1.2.3.4"}
	tgt.SetTraceHops(hops)
	view := tgt.GetView()
	if len(view.TraceHops) != len(hops) {
		t.Fatalf("TraceHops len: got %d, want %d", len(view.TraceHops), len(hops))
	}
	for i, h := range hops {
		if view.TraceHops[i] != h {
			t.Errorf("TraceHops[%d]: got %q, want %q", i, view.TraceHops[i], h)
		}
	}
	// Verify it's a copy (mutation should not affect stored value)
	hops[0] = "mutated"
	if tgt.GetView().TraceHops[0] == "mutated" {
		t.Error("SetTraceHops should store a copy, not a reference")
	}
}

func TestTargetStats_SetIfaceMTU(t *testing.T) {
	tgt := NewTargetStats("example.com")
	tgt.SetIfaceMTU(1500)
	if tgt.GetView().IfaceMTU != 1500 {
		t.Errorf("SetIfaceMTU: got %d, want 1500", tgt.GetView().IfaceMTU)
	}
}

func TestTargetStats_SetPMTU(t *testing.T) {
	tgt := NewTargetStats("example.com")
	tgt.SetPMTU(1400)
	if tgt.GetView().PMTU != 1400 {
		t.Errorf("SetPMTU: got %d, want 1400", tgt.GetView().PMTU)
	}
}

func TestSetPMTUBottleneckIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{"non-empty IP", "10.0.0.1"},
		{"empty string", ""},
		{"another IP", "192.168.1.254"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tgt := NewTargetStats("example.com")
			tgt.SetPMTUBottleneckIP(tt.ip)
			view := tgt.GetView()
			if view.PMTUBottleneckIP != tt.ip {
				t.Errorf("PMTUBottleneckIP: got %q, want %q", view.PMTUBottleneckIP, tt.ip)
			}
		})
	}
}

func TestTargetStatsFailureAndReset(t *testing.T) {
	tgt := NewTargetStats("example.com")
	tgt.IncSent()
	tgt.OnFailure("Timeout")

	view := tgt.GetView()
	if view.Loss != 1 {
		t.Fatalf("Loss: got %d, want 1", view.Loss)
	}
	if view.LastLossTime.IsZero() {
		t.Fatalf("LastLossTime should be set")
	}
	if view.LastError != "Timeout" {
		t.Fatalf("LastError: got %q, want %q", view.LastError, "Timeout")
	}

	tgt.Reset()
	view = tgt.GetView()
	if view.Sent != 0 || view.Recv != 0 || view.Loss != 0 {
		t.Fatalf("Reset did not clear counts: sent=%d recv=%d loss=%d", view.Sent, view.Recv, view.Loss)
	}
	if view.LastRTT != 0 || view.MinRTT != 0 || view.MaxRTT != 0 || view.AvgRTT != 0 || view.Jitter != 0 {
		t.Fatalf("Reset did not clear RTT stats: last=%v min=%v max=%v avg=%v jitter=%v",
			view.LastRTT, view.MinRTT, view.MaxRTT, view.AvgRTT, view.Jitter)
	}
	if len(view.History) != 0 {
		t.Fatalf("Reset did not clear history: len=%d", len(view.History))
	}
	if !view.LastLossTime.IsZero() || view.LastError != "" {
		t.Fatalf("Reset did not clear error state: lastLoss=%v lastError=%q", view.LastLossTime, view.LastError)
	}
}
