package stats

import (
	"testing"
	"time"
)

// durs is a small helper to build a []time.Duration from plain ints
// (milliseconds), keeping the table below readable.
func durs(ms ...int) []time.Duration {
	out := make([]time.Duration, len(ms))
	for i, m := range ms {
		out[i] = time.Duration(m) * time.Millisecond
	}
	return out
}

func TestReconstructHistoryWindow_NotYetWrapped(t *testing.T) {
	// Ring not yet full: length < size, so entries are simply buf[:length]
	// in chronological order — mirrors reconstructHistory's own fast path.
	buf := make([]time.Duration, 10)
	copy(buf, durs(1, 2, 3, 4, 5))
	idx := 5
	length := 5

	tests := []struct {
		name string
		n    int
		want []time.Duration
	}{
		{"n smaller than length", 3, durs(3, 4, 5)},
		{"n equal to length", 5, durs(1, 2, 3, 4, 5)},
		{"n larger than length", 100, durs(1, 2, 3, 4, 5)},
		{"n is zero", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reconstructHistoryWindow(buf, idx, length, tt.n)
			assertDurationsEqual(t, got, tt.want)
		})
	}
}

func TestReconstructHistoryWindow_Wrapped(t *testing.T) {
	// Ring full and wrapped: size=5, 8 total appends means the buffer holds
	// [6,7,8,4,5] physically, idx=8%5=3 is both the next-write slot and the
	// oldest entry's position; chronological order is [4,5,6,7,8].
	buf := make([]time.Duration, 5)
	copy(buf, durs(6, 7, 8, 4, 5))
	idx := 8
	length := 5

	tests := []struct {
		name string
		n    int
		want []time.Duration
	}{
		{"window within one segment (no wrap in window)", 2, durs(7, 8)},
		{"window straddling the wrap point", 3, durs(6, 7, 8)},
		{"window equal to full ring", 5, durs(4, 5, 6, 7, 8)},
		{"window larger than ring", 20, durs(4, 5, 6, 7, 8)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reconstructHistoryWindow(buf, idx, length, tt.n)
			assertDurationsEqual(t, got, tt.want)
		})
	}
}

func TestReconstructHistoryWindow_Empty(t *testing.T) {
	if got := reconstructHistoryWindow(nil, 0, 0, 10); got != nil {
		t.Errorf("expected nil for empty history, got %v", got)
	}
	buf := make([]time.Duration, 5)
	if got := reconstructHistoryWindow(buf, 0, 0, 10); got != nil {
		t.Errorf("expected nil for zero length, got %v", got)
	}
}

func TestRttAccumulator_HistorySnapshotWindow_MatchesFullSnapshotTail(t *testing.T) {
	var a rttAccumulator
	for i := 1; i <= historySize+50; i++ {
		a.appendHistory(time.Duration(i)*time.Millisecond, historySize)
	}

	full := a.historySnapshot()
	window := a.historySnapshotWindow(30)

	if len(window) != 30 {
		t.Fatalf("window length: got %d, want 30", len(window))
	}
	wantTail := full[len(full)-30:]
	assertDurationsEqual(t, window, wantTail)
}

func TestRttAccumulator_HistorySnapshotWindow_FewerSamplesThanWindow(t *testing.T) {
	var a rttAccumulator
	a.appendHistory(1*time.Millisecond, historySize)
	a.appendHistory(2*time.Millisecond, historySize)

	got := a.historySnapshotWindow(30)
	assertDurationsEqual(t, got, durs(1, 2))
}

func assertDurationsEqual(t *testing.T, got, want []time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length: got %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %v, want %v (full got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}
