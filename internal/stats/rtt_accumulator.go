package stats

import (
	"math"
	"time"
)

// rttAccumulator tracks min/max/sum/sample-count RTT statistics and an
// optional ring-buffer history. Shared by TargetStats, PortCheckResult, and
// HTTPCheckResult.
// Not safe for concurrent use; callers hold their own mutex.
type rttAccumulator struct {
	min     time.Duration
	max     time.Duration
	sum     time.Duration
	samples int

	// mean and m2 hold Welford's online algorithm state (Knuth, TAOCP vol.
	// 2, §4.2.2) for computing variance one sample at a time without ever
	// squaring the raw RTT sum. This is deliberately NOT ping.c/ping6.c's
	// `vari := tsumsq/n - avg*avg`: that formula subtracts two large,
	// nearly-equal float64 values, and when RTTs are large relative to
	// their spread the rounding error in that subtraction can exceed the
	// true (small) variance, driving vari negative and sqrt(vari) to NaN.
	// Values are float64 nanoseconds (time.Duration's unit).
	mean float64
	m2   float64

	// history is nil for accumulators that don't track a ring buffer
	// (HTTPCheckResult has no History field in its view). historyIdx/
	// historyLen track total writes and the number of retained samples.
	history    []time.Duration
	historyIdx int
	historyLen int
}

// record updates min/max/sum/samples for a successful RTT measurement.
// No-op for rtt <= 0; the Welford variance state must stay inside this
// guard too, so a failed probe (rtt <= 0) never perturbs stddev().
func (a *rttAccumulator) record(rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	a.samples++
	a.sum += rtt
	if a.min == 0 || rtt < a.min {
		a.min = rtt
	}
	if rtt > a.max {
		a.max = rtt
	}

	x := float64(rtt)
	delta := x - a.mean
	a.mean += delta / float64(a.samples)
	delta2 := x - a.mean
	a.m2 += delta * delta2
}

// avg returns sum/samples, or 0 if no samples have been recorded.
func (a *rttAccumulator) avg() time.Duration {
	if a.samples == 0 {
		return 0
	}
	return a.sum / time.Duration(a.samples)
}

// stddev returns the population standard deviation of recorded RTTs (i.e.
// variance divided by n, matching ping.c/ping6.c's "round-trip
// min/avg/max/std-dev" semantics), computed from the Welford state
// accumulated in record(). Returns 0 for 0 or 1 samples, where variance is
// undefined/zero.
func (a *rttAccumulator) stddev() time.Duration {
	if a.samples < 2 {
		return 0
	}
	variance := a.m2 / float64(a.samples)
	return time.Duration(math.Sqrt(variance))
}

// appendHistory pushes rtt (which may be 0, e.g. to mark a failed probe)
// into the ring buffer, lazily allocating it at size capacity on first use.
func (a *rttAccumulator) appendHistory(rtt time.Duration, capacity int) {
	if a.history == nil {
		a.history = make([]time.Duration, capacity)
	}
	a.history[a.historyIdx%capacity] = rtt
	a.historyIdx++
	if a.historyLen < capacity {
		a.historyLen++
	}
}

// historySnapshotWindow copies up to n trailing samples in chronological order.
// historyIdx counts writes, so the tail starts n slots before the next write
// and spans at most two contiguous parts of the buffer.
func (a *rttAccumulator) historySnapshotWindow(n int) []time.Duration {
	n = min(n, a.historyLen)
	if n <= 0 {
		return nil
	}
	start := (a.historyIdx - n) % len(a.history)
	out := make([]time.Duration, n)
	copied := copy(out, a.history[start:])
	copy(out[copied:], a.history)
	return out
}

// updateJitter applies one RFC 1889 §A.8 smoothing step and returns the new
// jitter value in nanoseconds. Shared by TargetStats.OnSuccess and
// MTRStats.RecordReply; each caller keeps its own "is this the first sample"
// gate since the two types increment their receive counters at different
// points relative to this call.
func updateJitter(jitter int64, rtt, lastRTT time.Duration) int64 {
	delta := int64(rtt - lastRTT)
	if delta < 0 {
		delta = -delta
	}
	return jitter + (delta-jitter)/jitterDivisor
}
