package web

import (
	"net"
	"net/http"
	"strconv"
	"testing"
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

func TestCrossOriginRequestsAreRejected(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	tests := []struct {
		origin string
		want   int
	}{
		{"", http.StatusOK},
		{"http://127.0.0.1:8080", http.StatusOK},
		{"http://localhost:8080", http.StatusOK},
		{"https://evil.example", http.StatusForbidden},
		{"http://localhost.evil.example", http.StatusForbidden},
		{"null", http.StatusForbidden},
		{"://bad", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
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
