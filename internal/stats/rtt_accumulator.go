package stats

import (
	"math"
	"time"
)

// rttAccumulator tracks min/max/sum/sample-count RTT statistics and an
// optional ring-buffer history. Shared by TargetStats, PortCheckResult, and
// HTTPCheckResult, which previously each re-implemented this bookkeeping.
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
	// historyLen follow the layout reconstructHistory expects.
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
// into the ring buffer, lazily allocating it at size cap on first use.
func (a *rttAccumulator) appendHistory(rtt time.Duration, cap int) {
	if a.history == nil {
		a.history = make([]time.Duration, cap)
	}
	a.history[a.historyIdx%cap] = rtt
	a.historyIdx++
	if a.historyLen < cap {
		a.historyLen++
	}
}

// historySnapshot returns an ordered copy of the ring buffer for UI/export.
func (a *rttAccumulator) historySnapshot() []time.Duration {
	return reconstructHistory(a.history, a.historyIdx, a.historyLen)
}

// historySnapshotWindow returns only the trailing n entries (or fewer, if
// fewer than n have been recorded), avoiding the full-buffer copy
// historySnapshot always does. Intended for callers like the RTT graph that
// only ever display a fixed trailing window regardless of how large the
// underlying ring buffer is.
func (a *rttAccumulator) historySnapshotWindow(n int) []time.Duration {
	return reconstructHistoryWindow(a.history, a.historyIdx, a.historyLen, n)
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

// reconstructHistory rebuilds an ordered slice from a ring buffer.
// buf is the raw buffer, idx is the write pointer, length is the count of valid entries.
func reconstructHistory(buf []time.Duration, idx, length int) []time.Duration {
	if length == 0 || buf == nil {
		return nil
	}
	size := len(buf)
	if length < size {
		out := make([]time.Duration, length)
		copy(out, buf[:length])
		return out
	}
	start := idx % size
	out := make([]time.Duration, size)
	copy(out, buf[start:])
	copy(out[size-start:], buf[:start])
	return out
}

// reconstructHistoryWindow returns only the trailing min(n, length) entries
// (oldest-to-newest order, same convention as reconstructHistory), without
// allocating or copying the full ring first. Used by callers (e.g. the RTT
// graph) that only ever need a fixed trailing window rather than the whole
// multi-thousand-entry history — historySize is sized for the longest
// possible display window, but most callers need far fewer points.
func reconstructHistoryWindow(buf []time.Duration, idx, length, n int) []time.Duration {
	if length == 0 || buf == nil || n <= 0 {
		return nil
	}
	size := len(buf)
	effLen := min(n, length)
	if length < size {
		// No wraparound yet: buf[:length] is already chronological order.
		out := make([]time.Duration, effLen)
		copy(out, buf[length-effLen:length])
		return out
	}
	// Full ring buffer: idx%size is both the next-write position and the
	// oldest entry's position. The trailing effLen entries occupy the
	// effLen slots immediately before idx%size, wrapping at most once.
	windowStart := ((idx-effLen)%size + size) % size
	out := make([]time.Duration, effLen)
	if windowStart+effLen <= size {
		copy(out, buf[windowStart:windowStart+effLen])
	} else {
		first := size - windowStart
		copy(out, buf[windowStart:size])
		copy(out[first:], buf[:effLen-first])
	}
	return out
}
