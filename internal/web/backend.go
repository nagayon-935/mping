package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/nagayon-935/mping/internal/stats"
)

// newBackendHandler serves the measurement API from src: statistics,
// history and events, and the controls, which need token. It checks neither
// Host nor Origin and serves no page, so it must only be reachable through
// the frontend (newFrontendHandler): in mping, over a UNIX socket in a
// directory only the web UI process can open.
func newBackendHandler(src *Source, token string, generation func() uint64) http.Handler {
	if generation == nil {
		generation = stats.Generation
	}
	cache := newSnapshotCache(src, generation)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/snapshot", handleSnapshot(cache))
	mux.HandleFunc("GET /api/v1/history", handleBulkHistory(src))
	mux.HandleFunc("GET /api/v1/events", handleBulkEvents(src))
	mux.HandleFunc("GET /api/v1/targets/{id}/history", handleHistory(src))
	mux.HandleFunc("GET /api/v1/targets/{id}/events", handleEvents(src))
	mux.HandleFunc("GET /api/v1/session", handleSession(src, token))
	mux.HandleFunc("POST /api/v1/targets", controlled(src, token, handleAddHost))
	mux.HandleFunc("DELETE /api/v1/targets/{id}", controlled(src, token, handleDeleteTarget))
	mux.HandleFunc("POST /api/v1/reset", controlled(src, token, handleReset))
	mux.HandleFunc("POST /api/v1/stop", controlled(src, token, handleStop))
	mux.HandleFunc("POST /api/v1/restart", controlled(src, token, handleRestart))
	mux.HandleFunc("GET /api/v1/report", controlled(src, token, handleReport))
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	return mux
}

// Backend serves the measurement API on a listener the caller owns.
type Backend struct {
	srv       *http.Server
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// ServeBackend serves src's API on ln, typically a UNIX socket that only the
// web UI process can reach, until Close. Control requests must carry token.
func ServeBackend(ln net.Listener, src *Source, token string) (*Backend, error) {
	if src == nil {
		return nil, fmt.Errorf("web: nil Source")
	}
	if err := CheckToken(token); err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	b := &Backend{
		srv:  newHTTPServer(newBackendHandler(src, token, nil)),
		done: make(chan struct{}),
	}
	go func() {
		defer close(b.done)
		_ = b.srv.Serve(ln)
	}()
	return b, nil
}

// Close stops serving and waits for in-flight requests, up to
// shutdownTimeout. Safe to call more than once.
func (b *Backend) Close() error {
	b.closeOnce.Do(func() {
		b.closeErr = closeHTTPServer(b.srv, b.done, "web: backend shutdown")
	})
	return b.closeErr
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
	meta, targets := p.Frame()
	body, err := json.Marshal(SnapshotResponse{
		State:    state,
		Meta:     meta,
		Snapshot: stats.BuildSnapshot(targets, p.HTTPResults()),
	})
	if err != nil {
		return nil, snapshotKey{}, fmt.Errorf("encode snapshot: %w", err)
	}
	c.ok, c.key, c.body = true, key, body
	return body, key, nil
}
