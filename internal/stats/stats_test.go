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
