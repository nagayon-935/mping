package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/nagayon-935/mping/internal/stats"
)

// maxHistoryPoints caps ?n= on the history endpoint; it matches the depth of
// the RTT ring each target keeps.
const maxHistoryPoints = 3000

const defaultHistoryPoints = 300

// SnapshotResponse is the body of /api/v1/snapshot and of each SSE
// "snapshot" event.
type SnapshotResponse struct {
	Reloading bool                 `json:"reloading"`
	Meta      Meta                 `json:"meta"`
	Snapshot  stats.ExportSnapshot `json:"snapshot"`
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

func buildSnapshot(src *Source) (SnapshotResponse, error) {
	p, reloading, _ := src.load()
	if p == nil {
		return SnapshotResponse{}, errNoProvider
	}
	return SnapshotResponse{
		Reloading: reloading,
		Meta:      p.Meta(),
		Snapshot:  stats.BuildSnapshot(p.Targets(), p.HTTPResults()),
	}, nil
}

func handleSnapshot(src *Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap, err := buildSnapshot(src)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeJSON(w, snap)
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
