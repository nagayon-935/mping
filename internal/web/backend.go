package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

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
	mux.HandleFunc("POST /api/v1/stop", controlled(src, token, handleMeasurement(false)))
	mux.HandleFunc("POST /api/v1/restart", controlled(src, token, handleMeasurement(true)))
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
		srv: &http.Server{
			Handler:           newBackendHandler(src, token, nil),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
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
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := b.srv.Shutdown(ctx); err != nil {
			b.closeErr = fmt.Errorf("web: backend shutdown: %w", err)
			_ = b.srv.Close()
		}
		<-b.done
	})
	return b.closeErr
}
