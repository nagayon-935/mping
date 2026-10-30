package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// socketPath returns a socket path short enough for sun_path (104 bytes on
// macOS), which t.TempDir() names can exceed.
func socketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mw")
	if err != nil {
		dir = t.TempDir()
	} else {
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	return filepath.Join(dir, name)
}

// startSplit runs the backend on a UNIX socket and the frontend in front of
// it, as mping does across two processes.
func startSplit(t *testing.T, p Provider) (*Source, *Server) {
	t.Helper()
	src := NewSource()
	src.Set(p)
	socket := socketPath(t, "backend.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := ServeBackend(ln, src, testToken)
	if err != nil {
		t.Fatal(err)
	}
	front, err := StartFrontend(FrontendOptions{Port: 0, Backend: socket})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		front.Close()
		backend.Close()
	})
	return src, front
}

func TestSplitFrontendRelaysTheAPIToTheBackendSocket(t *testing.T) {
	base, ts := providerWithTarget("a.example")
	p := &controlProvider{fakeProvider: base}
	_, front := startSplit(t, p)
	url := "http://" + front.Addr()

	if status, body := send(t, url, req{method: http.MethodGet, path: "/api/v1/snapshot"}); status != 200 || !strings.Contains(body, "a.example") {
		t.Fatalf("snapshot through the frontend = %d %s", status, body)
	}
	if status, _ := send(t, url, req{method: http.MethodPost, path: "/api/v1/reset"}); status != http.StatusForbidden {
		t.Fatalf("reset without the token = %d, want 403 from the backend", status)
	}
	if status, _ := send(t, url, req{method: http.MethodPost, path: "/api/v1/reset", headers: map[string]string{tokenHeader: testToken}}); status != http.StatusNoContent || p.resets != 1 {
		t.Fatalf("reset with the token = %d (resets %d)", status, p.resets)
	}
	// The frontend still refuses other sites before relaying anything.
	if resp := doWithHeaders(t, url+"/api/v1/snapshot", map[string]string{"Host": "attacker.example"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host = %d, want 403", resp.StatusCode)
	}

	_, frames := openStream(t, url)
	nextEvent(t, frames, 2*time.Second)
	ts.IncSent()
	ts.OnSuccess(time.Millisecond, 64)
	if got := decodeSnapshotEvent(t, nextEvent(t, frames, 3*time.Second)).Snapshot.Targets[0].Recv; got != 1 {
		t.Fatalf("streamed recv = %d, want 1", got)
	}
}

func TestSplitFrontendReportsAnUnreachableBackend(t *testing.T) {
	front, err := StartFrontend(FrontendOptions{Port: 0, Backend: socketPath(t, "missing.sock")})
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()

	status, body := send(t, "http://"+front.Addr(), req{method: http.MethodGet, path: "/api/v1/snapshot"})
	if status != http.StatusBadGateway || !strings.Contains(body, "not reachable") {
		t.Fatalf("snapshot with no backend = %d %s, want 502", status, body)
	}
}

func TestSnapshotHonoursItsETag(t *testing.T) {
	p, _ := providerWithTarget("a.example")
	src := NewSource()
	src.Set(p)
	srv := httptestServer(t, newBackendHandler(src, "", func() uint64 { return 7 }))

	first := get(t, srv+"/api/v1/snapshot")
	tag := first.Header.Get("ETag")
	if first.StatusCode != 200 || tag == "" {
		t.Fatalf("first snapshot = %d, ETag %q", first.StatusCode, tag)
	}
	again := doWithHeaders(t, srv+"/api/v1/snapshot", map[string]string{"If-None-Match": tag})
	if again.StatusCode != http.StatusNotModified {
		t.Fatalf("unchanged snapshot = %d, want 304", again.StatusCode)
	}
	src.MarkReloading()
	changed := doWithHeaders(t, srv+"/api/v1/snapshot", map[string]string{"If-None-Match": tag})
	if changed.StatusCode != 200 || changed.Header.Get("ETag") == tag {
		t.Fatalf("changed snapshot = %d, ETag %q", changed.StatusCode, changed.Header.Get("ETag"))
	}
}

func TestServeBackendRejectsAWeakToken(t *testing.T) {
	ln, err := net.Listen("unix", socketPath(t, "b.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := ServeBackend(ln, NewSource(), "short"); err == nil {
		t.Fatal("a short token was accepted")
	}
}

// httptestServer serves h over loopback HTTP for the test and returns its
// base URL.
func httptestServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}
