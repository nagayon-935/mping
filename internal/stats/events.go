package stats

import "time"

const maxTargetEvents = 128

// Event belongs to one immutable target identity, independent of UI rendering.
type Event struct {
	Count    int       `json:"count,omitempty"`
	LastAt   time.Time `json:"last_at,omitzero"`
	At       time.Time `json:"at"`
	TargetID uint64    `json:"target_id"`
	Kind     string    `json:"kind"`
	Message  string    `json:"message"`
}

func (t *TargetStats) recordEventLocked(kind, message string) {
	now := time.Now()
	e := Event{At: now, TargetID: t.ID, Kind: kind, Message: message, Count: 1, LastAt: now}
	if t.health.configured && kind == "ping" && len(t.events) > 0 {
		// Health transitions can sit between two identical probe failures.
		for i := len(t.events) - 1; i >= 0; i-- {
			last := &t.events[i]
			if last.Kind == "health" {
				continue
			}
			if last.Kind == kind && last.Message == message {
				last.Count++
				last.LastAt = now
				if t.eventSink != nil {
					t.eventSink(*last)
				}
				return
			}
			break
		}
	}
	t.events = append(t.events, e)
	if len(t.events) > maxTargetEvents {
		t.events = append([]Event(nil), t.events[len(t.events)-maxTargetEvents:]...)
		t.eventsDropped++
	}
	if t.eventSink != nil {
		t.eventSink(e)
	}
}

// The sink must return immediately; it is called under the target lock.
func (t *TargetStats) SetEventSink(sink func(Event)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eventSink = sink
}

func (t *TargetStats) RecordEvent(kind, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordEventLocked(kind, message)
	bumpGeneration()
}

func (t *TargetStats) Events() ([]Event, int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]Event(nil), t.events...), t.eventsDropped
}

// ReportData captures counters and their events under the same target lock.
func (t *TargetStats) ReportData() (TargetView, []Event, int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.viewLocked(t.rtt.historySnapshot), append([]Event(nil), t.events...), t.eventsDropped
}
