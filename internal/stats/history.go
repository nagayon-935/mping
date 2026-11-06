package stats

import "time"

// IPChange records when a target resolved to an IP address.
type IPChange struct {
	At time.Time `json:"at"`
	IP string    `json:"ip"`
}

// SetMeasurementState is independent of the HTTP server/session state.
func (t *TargetStats) SetMeasurementState(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.measurementState = state
	bumpGeneration()
}

// MeasurementState returns the measurement lifecycle state last recorded for the target.
func (t *TargetStats) MeasurementState() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.measurementState
}

// HistorySample preserves wall time. A nil RTT marks a lost probe.
type HistorySample struct {
	At    time.Time `json:"at"`
	RTTMS *float64  `json:"rtt_ms"`
}

func (t *TargetStats) appendHistoryLocked(rtt time.Duration, at time.Time) {
	t.rtt.appendHistory(rtt, historySize)
	if t.historyTimes == nil {
		t.historyTimes = make([]int64, historySize)
	}
	t.historyTimes[(t.rtt.historyIdx-1)%historySize] = at.UnixNano()
}

// HistorySamples returns up to n trailing ping samples in chronological order.
// Non-positive n returns an empty slice; samples without a recorded wall time are omitted.
func (t *TargetStats) HistorySamples(n int) []HistorySample {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if n > t.rtt.historyLen {
		n = t.rtt.historyLen
	}
	if n < 0 {
		n = 0
	}
	out := make([]HistorySample, 0, n)
	for i := t.rtt.historyLen - n; i < t.rtt.historyLen; i++ {
		idx := (t.rtt.historyIdx - t.rtt.historyLen + i) % historySize
		if t.historyTimes == nil || t.historyTimes[idx] == 0 {
			continue
		}
		sample := HistorySample{At: time.Unix(0, t.historyTimes[idx]).UTC()}
		if d := t.rtt.history[idx]; d > 0 {
			ms := float64(d) / float64(time.Millisecond)
			sample.RTTMS = &ms
		}
		out = append(out, sample)
	}
	return out
}
