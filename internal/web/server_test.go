package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func doWithHeaders(t *testing.T, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestHostHeaderMustBeLoopback(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	tests := []struct {
		host string
		want int
	}{
		{"127.0.0.1:8080", http.StatusOK},
		{"localhost:8080", http.StatusOK},
		{"localhost", http.StatusOK},
		{"[::1]:8080", http.StatusOK},
		{"LOCALHOST:8080", http.StatusOK},
		{"evil.example", http.StatusForbidden},
		{"evil.example:8080", http.StatusForbidden},
		{"127.0.0.1.evil.example", http.StatusForbidden},
		{"192.168.1.10:8080", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			resp := doWithHeaders(t, srv.URL+"/", map[string]string{"Host": tt.host})

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestOriginMustMatchRequestedHostExactly(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())
	host := strings.TrimPrefix(srv.URL, "http://")
	_, port, _ := net.SplitHostPort(host)

	tests := []struct {
		name   string
		origin string
		want   int
	}{
		{"no origin", "", http.StatusOK},
		{"same origin", "http://" + host, http.StatusOK},
		{"other loopback port", "http://127.0.0.1:1", http.StatusForbidden},
		{"localhost alias of same port", "http://localhost:" + port, http.StatusForbidden},
		{"https scheme", "https://" + host, http.StatusForbidden},
		{"foreign site", "https://evil.example", http.StatusForbidden},
		{"lookalike host", "http://localhost.evil.example", http.StatusForbidden},
		{"opaque origin", "null", http.StatusForbidden},
		{"malformed", "://bad", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{}
			if tt.origin != "" {
				headers["Origin"] = tt.origin
			}

			resp := doWithHeaders(t, srv.URL+"/", headers)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestOriginMatchUsesHostHeaderTheBrowserSent(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	resp := doWithHeaders(t, srv.URL+"/", map[string]string{
		"Host":   "localhost:" + port,
		"Origin": "http://localhost:" + port,
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a page opened via http://localhost", resp.StatusCode)
	}
}

func TestStaticHandlerHidesDirectoryListings(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<title>x</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	srv := httptest.NewServer(staticHandler(fsys))
	t.Cleanup(srv.Close)

	tests := []struct {
		path string
		want int
	}{
		{"/", http.StatusOK},
		{"/assets/app.js", http.StatusOK},
		{"/assets/", http.StatusNotFound},
		{"/assets", http.StatusNotFound},
		{"/missing.js", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp := get(t, srv.URL+tt.path)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestStartAlsoListensOnIPv6Loopback(t *testing.T) {
	probe, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	probe.Close()
	s, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	_, port, _ := net.SplitHostPort(s.Addr())

	resp := get(t, "http://[::1]:"+port+"/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 over [::1]", resp.StatusCode)
	}
}

func TestResponsesCarrySecurityHeaders(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	resp := get(t, srv.URL+"/")

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy is missing")
	}
}

func TestStartListensOnLoopbackOnly(t *testing.T) {
	s, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	host, _, err := net.SplitHostPort(s.Addr())

	if err != nil {
		t.Fatalf("Addr %q: %v", s.Addr(), err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("listening on %q, want a loopback address", host)
	}
}

func TestStartServesAndReportsURL(t *testing.T) {
	s, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	resp := get(t, s.URL())

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200", s.URL(), resp.StatusCode)
	}
}

func TestStartFailsWhenPortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = Start(Options{Port: port, Source: NewSource()})

	if err == nil {
		t.Fatalf("Start on taken port %d succeeded, want an error", port)
	}
}

func TestStartRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{"negative port", Options{Port: -1, Source: NewSource()}},
		{"port above range", Options{Port: 65536, Source: NewSource()}},
		{"nil source", Options{Port: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Start(tt.opts)

			if err == nil {
				s.Close()
				t.Fatal("Start succeeded, want an error")
			}
		})
	}
}

func TestCloseEndsOpenStreams(t *testing.T) {
	s, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, frames := openStream(t, "http://"+s.Addr())

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked for 2s with a stream open")
	}
	for range frames {
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	first := s.Close()
	second := s.Close()

	if first != nil || second != nil {
		t.Fatalf("Close errors = %v, %v; want nil, nil", first, second)
	}
}

func TestURLUsesPortChosenByKernel(t *testing.T) {
	s, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	_, portStr, _ := net.SplitHostPort(s.Addr())

	port, _ := strconv.Atoi(portStr)

	if port == 0 {
		t.Fatal("Addr still reports port 0")
	}
	if want := "http://127.0.0.1:" + portStr + "/"; s.URL() != want {
		t.Fatalf("URL = %q, want %q", s.URL(), want)
	}
}

func TestDashboardAssetsAreServedWithBrowserTypes(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	tests := []struct {
		path, wantType string
	}{
		{"/style.css", "text/css"},
		{"/js/app.js", "text/javascript"},
		{"/js/model.js", "text/javascript"},
		{"/js/table.js", "text/javascript"},
		{"/js/graphs.js", "text/javascript"},
		{"/js/inspect.js", "text/javascript"},
		{"/js/timeline.js", "text/javascript"},
		{"/js/columns.js", "text/javascript"},
		{"/js/cells.js", "text/javascript"},
		{"/js/control.js", "text/javascript"},
		{"/js/chart.js", "text/javascript"},
		{"/js/dom.js", "text/javascript"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp := get(t, srv.URL+tt.path)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, tt.wantType) {
				t.Fatalf("Content-Type = %q, want %s", ct, tt.wantType)
			}
		})
	}
}

func TestJSTestsAreNotEmbedded(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	resp := get(t, srv.URL+"/jstest/model.test.mjs")

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (test files must stay out of the binary)", resp.StatusCode)
	}
}

// TestCloseDeliversStoppedStateToOpenStreams mirrors mping's exit path:
// MarkStopped is followed immediately by Close, well inside one stream poll
// interval, and the browser must still learn the run is over.
func TestCloseDeliversStoppedStateToOpenStreams(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	s, err := Start(Options{Port: 0, Source: src})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, frames := openStream(t, "http://"+s.Addr())
	nextEvent(t, frames, 2*time.Second)

	src.MarkStopped()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	body := decodeSnapshotEvent(t, nextEvent(t, frames, time.Second))

	if body.State != StateStopped {
		t.Fatalf("final state = %q, want %q", body.State, StateStopped)
	}
}
