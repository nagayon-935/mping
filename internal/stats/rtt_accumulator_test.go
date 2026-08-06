package stats

import (
	"math"
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

// ---- stddev (Welford's online algorithm) ----

func TestRttAccumulator_StdDev_KnownDataset(t *testing.T) {
	// Classic textbook dataset: population stddev of {2,4,4,4,5,5,7,9} is
	// exactly 2 (mean=5, sum of squared deviations=32, 32/8=4, sqrt(4)=2).
	var a rttAccumulator
	for _, ms := range []int{2, 4, 4, 4, 5, 5, 7, 9} {
		a.record(time.Duration(ms) * time.Millisecond)
	}

	got := a.stddev()
	want := 2 * time.Millisecond
	if got != want {
		t.Errorf("stddev() = %v, want %v", got, want)
	}
}

func TestRttAccumulator_StdDev_ZeroSamples(t *testing.T) {
	var a rttAccumulator
	if got := a.stddev(); got != 0 {
		t.Errorf("stddev() with 0 samples = %v, want 0", got)
	}
}

func TestRttAccumulator_StdDev_OneSample(t *testing.T) {
	var a rttAccumulator
	a.record(50 * time.Millisecond)
	if got := a.stddev(); got != 0 {
		t.Errorf("stddev() with 1 sample = %v, want 0", got)
	}
}

func TestRttAccumulator_StdDev_IgnoresNonPositiveRTT(t *testing.T) {
	// rtt<=0 probes (failed pings) must not perturb the Welford state,
	// mirroring the guard on min/max/sum in record().
	var withNoise, clean rttAccumulator
	values := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}

	for _, v := range values {
		clean.record(v)
	}

	withNoise.record(0)
	withNoise.record(-5 * time.Millisecond)
	for _, v := range values {
		withNoise.record(v)
	}
	withNoise.record(0)

	if got, want := withNoise.samples, clean.samples; got != want {
		t.Fatalf("samples = %d, want %d (non-positive RTTs were counted)", got, want)
	}
	if got, want := withNoise.stddev(), clean.stddev(); got != want {
		t.Errorf("stddev() = %v, want %v (non-positive RTTs perturbed Welford state)", got, want)
	}
}

// TestRttAccumulator_StdDev_NaiveFormulaWouldOverflowToNaN reproduces the
// catastrophic-cancellation failure mode of ping.c/ping6.c's
// `vari := tsumsq/n - avg*avg; sqrt(vari)`: when RTTs are large (here, all
// close to 1 second, i.e. ~1e9 ns) and the true variance is tiny relative to
// avg*avg, the naive formula subtracts two nearly-equal float64 values whose
// individual rounding error swamps the true (small) difference, so the
// result can go negative and sqrt() returns NaN. This dataset was found by
// brute-force search over small integer-nanosecond jitter around a 1s base,
// filtered to land the naive variance comfortably below zero (rather than
// only a few ULP past it) on both amd64 and arm64 — an earlier dataset here
// sat within the ~1e18-magnitude subtraction's rounding-noise floor, so it
// went negative on arm64 but landed on exactly 0.0 on amd64, making the
// `naiveVari < 0` assertion below architecture-dependent (see CI: it passed
// on macOS/arm64 and failed on Linux/amd64 for that reason).
//
// The true population variance here is 1.359375 ns^2 (stddev ≈ 1.1659 ns):
// offsets from 1e9 ns are {7,10,8,10,10,8,10,8}, mean offset 8.875, sum of
// squared deviations 10.875, 10.875/8 = 1.359375.
func TestRttAccumulator_StdDev_NaiveFormulaWouldOverflowToNaN(t *testing.T) {
	offsets := []int64{7, 10, 8, 10, 10, 8, 10, 8}
	const base = int64(time.Second) // 1_000_000_000 ns

	// Reproduce ping.c's naive formula directly to confirm it fails here.
	var sum, sumsq float64
	for _, off := range offsets {
		x := float64(base + off)
		sum += x
		sumsq += x * x
	}
	n := float64(len(offsets))
	avg := sum / n
	naiveVari := sumsq/n - avg*avg
	if !(naiveVari < 0) {
		t.Fatalf("test dataset no longer reproduces catastrophic cancellation: naive vari=%v (want <0)", naiveVari)
	}
	if !math.IsNaN(math.Sqrt(naiveVari)) {
		t.Fatalf("expected naive sqrt(vari) to be NaN, got %v", math.Sqrt(naiveVari))
	}

	// The Welford-based implementation must not exhibit this failure.
	var a rttAccumulator
	for _, off := range offsets {
		a.record(time.Duration(base + off))
	}

	got := a.stddev()
	if got <= 0 {
		t.Fatalf("stddev() = %v, want a small positive duration (naive formula would have been NaN)", got)
	}
	// sqrt(1.359375) ≈ 1.1659ns; time.Duration truncates to whole nanoseconds.
	want := 1 * time.Nanosecond
	if got != want {
		t.Errorf("stddev() = %v, want %v", got, want)
	}
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
