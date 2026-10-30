package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// maxHistoryPoints caps ?n= on the history endpoint; it matches the depth of
// the RTT ring each target keeps.
const maxHistoryPoints = 3000

const defaultHistoryPoints = 300

// SnapshotResponse is the body of /api/v1/snapshot and of each SSE
// "snapshot" event.
type SnapshotResponse struct {
	State    State                `json:"state"`
	Meta     Meta                 `json:"meta"`
	Snapshot stats.ExportSnapshot `json:"snapshot"`
}

// HistoryResponse is the body of /api/v1/targets/{id}/history. RTTMs is
// oldest-first; a null entry is a lost probe.
type HistoryResponse struct {
	Samples    []stats.HistorySample `json:"samples"`
	CapturedAt time.Time             `json:"captured_at"`
	ID         uint64                `json:"id"`
	RTTMs      []*float64            `json:"rtt_ms"`
}

// BulkHistoryResponse is the body of /api/v1/history: one HistoryResponse
// per live target, in display order.
type BulkHistoryResponse struct {
	CapturedAt time.Time         `json:"captured_at"`
	Targets    []HistoryResponse `json:"targets"`
}

// EventsResponse is the body of /api/v1/targets/{id}/events.
type EventsResponse struct {
	ID      uint64        `json:"id"`
	Events  []stats.Event `json:"events"`
	Dropped int           `json:"dropped"`
}

// maxEvents caps ?limit= on /api/v1/events; defaultEvents is used without it.
const (
	maxEvents     = 1000
	defaultEvents = 200
)

// TargetEvent is one stats.Event labelled with its target's host, as listed
// by /api/v1/events.
type TargetEvent struct {
	stats.Event
	Host string `json:"host"`
}

// BulkEventsResponse is the body of /api/v1/events: every live target's
// recorded events, newest first.
type BulkEventsResponse struct {
	Events []TargetEvent `json:"events"`
}

// handleSnapshot serves the cached snapshot. Its ETag is the cache key, so
// the frontend's stream can ask for it conditionally and tell a new state
// from one it already sent even when the bytes are the same.
func handleSnapshot(cache *snapshotCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, key, err := cache.get()
		switch {
		case errors.Is(err, errNoProvider):
			writeError(w, http.StatusServiceUnavailable, err.Error())
		case err != nil:
			writeError(w, http.StatusInternalServerError, "failed to encode snapshot")
		default:
			tag := fmt.Sprintf(`"%d.%d"`, key.gen, key.version)
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			writeBody(w, body)
		}
	}
}

func handleHistory(src *Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, ok := queryInt(w, r, "n", defaultHistoryPoints, maxHistoryPoints)
		if !ok {
			return
		}
		t, ok := lookupTarget(w, r, src)
		if !ok {
			return
		}
		writeJSON(w, historyOf(t, n))
	}
}

// handleBulkHistory serves every target's trailing series in one response,
// so a dashboard of N sparklines costs one request instead of N.
func handleBulkHistory(src *Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, ok := queryInt(w, r, "n", defaultHistoryPoints, maxHistoryPoints)
		if !ok {
			return
		}
		p, ok := liveProvider(w, src)
		if !ok {
			return
		}
		targets := p.Targets()
		out := BulkHistoryResponse{Targets: make([]HistoryResponse, len(targets))}
		for i, t := range targets {
			out.Targets[i] = historyOf(t, n)
		}
		out.CapturedAt = time.Now().UTC()
		writeJSON(w, out)
	}
}

// queryInt parses an optional query parameter in [1, maxValue], answering
// 400 itself when it returns ok=false.
func queryInt(w http.ResponseWriter, r *http.Request, name string, def, maxValue int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxValue {
		writeError(w, http.StatusBadRequest, name+" must be an integer between 1 and "+strconv.Itoa(maxValue))
		return 0, false
	}
	return n, true
}

// liveProvider returns the current provider, answering 503 itself when
// mping has none (e.g. between hosts-file reloads).
func liveProvider(w http.ResponseWriter, src *Source) (Provider, bool) {
	p, _, _ := src.load()
	if p == nil {
		writeError(w, http.StatusServiceUnavailable, errNoProvider.Error())
		return nil, false
	}
	return p, true
}

// targetID parses the {id} path value, answering 400 itself when it returns
// ok=false.
func targetID(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "target id must be an unsigned integer")
		return 0, false
	}
	return id, true
}

func historyOf(t *stats.TargetStats, n int) HistoryResponse {
	samples := t.HistorySamples(n)
	rtt := make([]*float64, len(samples))
	for i, sample := range samples {
		// HistorySamples builds a fresh slice with fresh RTT pointers per
		// call and nothing mutates them afterwards, so sharing is safe.
		rtt[i] = sample.RTTMS
	}
	return HistoryResponse{ID: t.ID(), RTTMs: rtt, Samples: samples, CapturedAt: time.Now().UTC()}
}

// handleBulkEvents merges every live target's events into one newest-first
// list, the browser's counterpart to the TUI's Log pane.
func handleBulkEvents(src *Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, ok := queryInt(w, r, "limit", defaultEvents, maxEvents)
		if !ok {
			return
		}
		p, ok := liveProvider(w, src)
		if !ok {
			return
		}
		all := []TargetEvent{}
		for _, t := range p.Targets() {
			host := t.DisplayName()
			events, _ := t.Events()
			for _, e := range events {
				all = append(all, TargetEvent{Event: e, Host: host})
			}
		}
		slices.SortStableFunc(all, func(a, b TargetEvent) int { return b.At.Compare(a.At) })
		if len(all) > limit {
			all = all[:limit]
		}
		writeJSON(w, BulkEventsResponse{Events: all})
	}
}

func handleEvents(src *Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, ok := lookupTarget(w, r, src)
		if !ok {
			return
		}
		events, dropped := t.Events()
		if events == nil {
			events = []stats.Event{}
		}
		writeJSON(w, EventsResponse{ID: t.ID(), Events: events, Dropped: dropped})
	}
}

// lookupTarget resolves the {id} path value against the live targets,
// writing the error response itself when it returns ok=false.
func lookupTarget(w http.ResponseWriter, r *http.Request, src *Source) (*stats.TargetStats, bool) {
	p, ok := liveProvider(w, src)
	if !ok {
		return nil, false
	}
	id, ok := targetID(w, r)
	if !ok {
		return nil, false
	}
	for _, t := range p.Targets() {
		if t.ID() == id {
			return t, true
		}
	}
	writeError(w, http.StatusNotFound, "no such target")
	return nil, false
}

func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	writeBody(w, body)
}

func writeBody(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(body)
}
