package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// fakeProvider is a fixed Provider for handler tests. targetCalls counts
// Targets calls so tests can tell whether a snapshot was rebuilt or served
// from cache.
type fakeProvider struct {
	targets     []*stats.TargetStats
	http        []*stats.HTTPCheckResult
	meta        Meta
	targetCalls atomic.Int64
}

func (f *fakeProvider) Targets() []*stats.TargetStats {
	f.targetCalls.Add(1)
	return f.targets
}
func (f *fakeProvider) HTTPResults() []*stats.HTTPCheckResult { return f.http }
func (f *fakeProvider) Meta() Meta                            { return f.meta }

// newTestServer serves newHandler over httptest, cancelling the handler's
// lifetime context when the test ends.
func newTestServer(t *testing.T, src *Source, cfg handlerConfig) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newTestServerWithContext(t, ctx, src, cfg)
}

// newTestServerWithContext is newTestServer with a caller-owned handler
// lifetime context, for tests that end the server's streams themselves.
func newTestServerWithContext(t *testing.T, ctx context.Context, src *Source, cfg handlerConfig) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newHandler(ctx, src, cfg))
	// Cleanups run LIFO, so this may run before the caller's cancel: drop
	// client connections first so open streams see their request context end
	// instead of keeping Close blocked.
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return srv
}

func defaultTestConfig() handlerConfig {
	return handlerConfig{streamInterval: 5 * time.Millisecond, heartbeatInterval: time.Hour}
}

// get issues a GET with a loopback Host header (httptest's own address
// already is one, so this is just http.Get with t.Fatal on transport error).
func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// startServer runs Start on an ephemeral port with an empty Source and
// closes the server when the test ends.
func startServer(t *testing.T) *Server {
	t.Helper()
	s, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func providerWithTarget(host string) (*fakeProvider, *stats.TargetStats) {
	ts := stats.NewTargetStats(host)
	ts.SetIP("192.0.2.1")
	return &fakeProvider{
		targets: []*stats.TargetStats{ts},
		meta: Meta{
			IntervalMs: 1000,
			Features:   Features{MTR: true},
			Thresholds: Thresholds{RTTWarnMs: 50, RTTCritMs: 200},
			Groups:     []Group{{Name: "core", TargetIDs: []uint64{ts.ID}}},
		},
	}, ts
}
