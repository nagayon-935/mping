package stats

import "time"

// Probe owns updates for one operation in a particular statistics window.
// Reset invalidates existing Probes atomically with clearing the counters;
// a delayed send, reply, timeout or duplicate cannot enter the new window.
// The zero value is invalid and ignores all updates.
type Probe struct {
	target *TargetStats
	epoch  uint64
}

// NewProbe captures the statistics window before starting an operation.
func (t *TargetStats) NewProbe() Probe {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return Probe{target: t, epoch: t.probeEpoch}
}

func (p Probe) update(f func(*TargetStats)) bool {
	t := p.target
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if p.epoch != t.probeEpoch {
		return false
	}
	f(t)
	bumpGeneration()
	return true
}

// IncSent records a successful send in this probe's window.
func (p Probe) IncSent() bool {
	return p.update(func(t *TargetStats) { t.Sent++ })
}

// OnSuccess records the reply and its traffic class in one atomic update.
func (p Probe) OnSuccess(rtt time.Duration, ttl, dscp int) bool {
	return p.update(func(t *TargetStats) {
		t.onSuccessLocked(rtt, ttl)
		t.LastDSCP = dscp
	})
}

// OnFailure records a send error, DNS error, ICMP error or timeout.
func (p Probe) OnFailure(reason string) bool {
	return p.update(func(t *TargetStats) { t.onFailureLocked(reason) })
}

// OnDuplicate records a duplicate belonging to this probe's window.
func (p Probe) OnDuplicate() bool {
	return p.update(func(t *TargetStats) { t.Duplicates++ })
}

// OnLateReply records a late reply belonging to this probe's window.
func (p Probe) OnLateReply() bool {
	return p.update(func(t *TargetStats) { t.LateReplies++ })
}

// OnCancelled accounts for an outstanding probe discarded by a user operation.
// It does not alter loss, RTT, or last-error state.
func (p Probe) OnCancelled() bool {
	return p.update(func(t *TargetStats) { t.Cancelled++ })
}
