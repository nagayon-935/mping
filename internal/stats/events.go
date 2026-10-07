package stats

import "time"

const maxTargetEvents = 128

// Event belongs to one immutable target identity, independent of UI rendering.
type Event struct {
	At       time.Time `json:"at"`
	TargetID uint64    `json:"target_id"`
	Kind     string    `json:"kind"`
	Message  string    `json:"message"`
}

func (t *TargetStats) recordEventLocked(kind, message string) {
	t.events = append(t.events, Event{At: time.Now(), TargetID: t.ID, Kind: kind, Message: message})
	if len(t.events) > maxTargetEvents {
		t.events = append([]Event(nil), t.events[len(t.events)-maxTargetEvents:]...)
		t.eventsDropped++
	}
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
