package stats

import "time"

// rttAccumulator tracks min/max/sum/sample-count RTT statistics and an
// optional ring-buffer history. Shared by TargetStats, PortCheckResult, and
// HTTPCheckResult, which previously each re-implemented this bookkeeping.
// Not safe for concurrent use; callers hold their own mutex.
type rttAccumulator struct {
	min     time.Duration
	max     time.Duration
	sum     time.Duration
	samples int

	// history is nil for accumulators that don't track a ring buffer
	// (HTTPCheckResult has no History field in its view). historyIdx/
	// historyLen follow the layout reconstructHistory expects.
	history    []time.Duration
	historyIdx int
	historyLen int
}

// record updates min/max/sum/samples for a successful RTT measurement.
// No-op for rtt <= 0 (mirrors the guard every prior recordRTT had).
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
}

// avg returns sum/samples, or 0 if no samples have been recorded.
func (a *rttAccumulator) avg() time.Duration {
	if a.samples == 0 {
		return 0
	}
	return a.sum / time.Duration(a.samples)
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
