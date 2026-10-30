package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// backendURL addresses the backend behind any transport; only its path
// matters to the backend's routes.
var backendURL = &url.URL{Scheme: "http", Host: "mping-backend"}

// newFrontendHandler is what the browser talks to: the dashboard files, the
// snapshot stream, and every other /api/ request relayed to the backend
// through backend. guard rejects requests from other sites before anything
// is relayed.
func newFrontendHandler(ctx context.Context, cfg handlerConfig, backend http.RoundTripper) http.Handler {
	snapshots := &snapshotFetcher{client: &http.Client{Transport: backend}, minAge: cfg.streamInterval / 2}
	relay := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(backendURL)
			r.Out.Host = backendURL.Host
		},
		Transport: backend,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadGateway, "mping is not reachable")
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/stream", handleStream(ctx, snapshots, cfg))
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		mux.Handle(method+" /api/", relay)
	}
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(fmt.Sprintf("web: embedded static dir missing: %v", err))
	}
	mux.Handle("GET /", staticHandler(static))
	return guard(mux)
}

// snapshotFetcher reads the backend's snapshot for every open stream, at
// most once per minAge, and keeps the backend's ETag so a stream can tell a
// new state from the one it already sent.
type snapshotFetcher struct {
	client *http.Client
	minAge time.Duration

	mu   sync.Mutex
	at   time.Time
	body []byte
	tag  string
	err  error
}

// get returns the latest snapshot and its tag; fresh skips the minAge reuse
// (the final state sent when the server stops).
func (f *snapshotFetcher) get(fresh bool) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !fresh && !f.at.IsZero() && time.Since(f.at) < f.minAge {
		return f.body, f.tag, f.err
	}
	f.at = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, backendURL.String()+"/api/v1/snapshot", nil)
	if err != nil {
		f.err = err
		return nil, "", err
	}
	if f.tag != "" {
		req.Header.Set("If-None-Match", f.tag)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		f.body, f.tag, f.err = nil, "", err
		return nil, "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		f.err = nil
	case http.StatusOK:
		f.body, f.err = io.ReadAll(resp.Body)
		f.tag = resp.Header.Get("ETag")
	default:
		f.body, f.tag, f.err = nil, "", fmt.Errorf("backend snapshot: %s", resp.Status)
	}
	return f.body, f.tag, f.err
}

// unixTransport reaches a backend served on the UNIX socket at path.
func unixTransport(path string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: 30 * time.Second,
	}
}

// handlerTransport answers requests with an in-process handler, so the
// frontend can reach the backend without a socket (Start, tests).
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body == nil {
		r.Body = http.NoBody
	}
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	t.h.ServeHTTP(rec, r)
	return &http.Response{
		Status: fmt.Sprintf("%d %s", rec.status, http.StatusText(rec.status)), StatusCode: rec.status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: rec.header, Body: io.NopCloser(&rec.body), ContentLength: int64(rec.body.Len()), Request: r,
	}, nil
}

// recorder is the minimal http.ResponseWriter handlerTransport needs.
type recorder struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.body.Write(b)
}
