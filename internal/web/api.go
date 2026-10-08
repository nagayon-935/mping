package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

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
	ID    uint64     `json:"id"`
	RTTMs []*float64 `json:"rtt_ms"`
}

// EventsResponse is the body of /api/v1/targets/{id}/events.
type EventsResponse struct {
	ID      uint64        `json:"id"`
	Events  []stats.Event `json:"events"`
	Dropped int           `json:"dropped"`
}

var errNoProvider = errors.New("statistics are not available yet")

// snapshotKey identifies one state of the world: a stats generation plus a
// Source version (provider swap or reload flag change).
type snapshotKey struct {
	gen, version uint64
}

// snapshotCache encodes one snapshot per snapshotKey and hands the same
// bytes to every caller, so N open streams cost one build per change rather
// than N. It is safe for concurrent use; the mutex also keeps concurrent
// callers from building the same snapshot twice.
type snapshotCache struct {
	src        *Source
	generation func() uint64

	mu   sync.Mutex
	ok   bool
	key  snapshotKey
	body []byte
}

func newSnapshotCache(src *Source, generation func() uint64) *snapshotCache {
	return &snapshotCache{src: src, generation: generation}
}

// get returns the encoded SnapshotResponse and the key it was built for.
func (c *snapshotCache) get() ([]byte, snapshotKey, error) {
	p, state, version := c.src.load()
	if p == nil {
		return nil, snapshotKey{}, errNoProvider
	}
	// Read the generation before building: a change racing with the build
	// then shows up as a new key on the next call instead of being lost.
	key := snapshotKey{gen: c.generation(), version: version}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && c.key == key {
		return c.body, key, nil
	}
	body, err := json.Marshal(SnapshotResponse{
		State:    state,
		Meta:     p.Meta(),
		Snapshot: buildExportSnapshot(p),
	})
	if err != nil {
		return nil, snapshotKey{}, fmt.Errorf("encode snapshot: %w", err)
	}
	c.ok, c.key, c.body = true, key, body
	return body, key, nil
}

// buildExportSnapshot reads views without RTT history: the export format
// never includes it, and copying each target's full ring every tick would
// be wasted work.
func buildExportSnapshot(p Provider) stats.ExportSnapshot {
	targets := p.Targets()
	views := make([]stats.TargetView, len(targets))
	for i, t := range targets {
		views[i] = t.GetViewWindow(0)
	}
	results := p.HTTPResults()
	httpViews := make([]stats.HTTPCheckView, len(results))
	for i, r := range results {
		httpViews[i] = r.GetView()
	}
	return stats.BuildSnapshotFromViews(views, httpViews)
}

func handleSnapshot(cache *snapshotCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _, err := cache.get()
		switch {
		case errors.Is(err, errNoProvider):
			writeError(w, http.StatusServiceUnavailable, err.Error())
		case err != nil:
			writeError(w, http.StatusInternalServerError, "failed to encode snapshot")
		default:
			writeBody(w, body)
		}
	}
}

func handleHistory(src *Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := defaultHistoryPoints
		if raw := r.URL.Query().Get("n"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > maxHistoryPoints {
				writeError(w, http.StatusBadRequest, "n must be an integer between 1 and "+strconv.Itoa(maxHistoryPoints))
				return
			}
			n = v
		}
		t, ok := lookupTarget(w, r, src)
		if !ok {
			return
		}
		view := t.GetViewWindow(n)
		rtt := make([]*float64, len(view.History))
		for i, d := range view.History {
			// The stats package records a lost probe as a zero RTT.
			if d > 0 {
				ms := float64(d.Microseconds()) / 1000
				rtt[i] = &ms
			}
		}
		writeJSON(w, HistoryResponse{ID: t.ID, RTTMs: rtt})
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
		writeJSON(w, EventsResponse{ID: t.ID, Events: events, Dropped: dropped})
	}
}

// lookupTarget resolves the {id} path value against the live targets,
// writing the error response itself when it returns ok=false.
func lookupTarget(w http.ResponseWriter, r *http.Request, src *Source) (*stats.TargetStats, bool) {
	p, _, _ := src.load()
	if p == nil {
		writeError(w, http.StatusServiceUnavailable, errNoProvider.Error())
		return nil, false
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "target id must be an unsigned integer")
		return nil, false
	}
	for _, t := range p.Targets() {
		if t.ID == id {
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
