package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sseFrame is one parsed server-sent message: a comment (heartbeat) or an
// event with its data payload.
type sseFrame struct {
	comment bool
	event   string
	data    string
}

// openStream connects to /api/v1/stream and returns a channel of parsed
// frames. The connection is torn down when the test ends.
func openStream(t *testing.T, baseURL string) (*http.Response, <-chan sseFrame) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	frames := make(chan sseFrame, 64)
	go func() {
		defer close(frames)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				frames <- cur
				cur = sseFrame{}
			case strings.HasPrefix(line, ":"):
				cur.comment = true
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return resp, frames
}

func nextEvent(t *testing.T, frames <-chan sseFrame, within time.Duration) sseFrame {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("stream closed before an event arrived")
			}
			if !f.comment {
				return f
			}
		case <-deadline:
			t.Fatalf("no event within %v", within)
		}
	}
}

func expectNoEvent(t *testing.T, frames <-chan sseFrame, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return
			}
			if !f.comment {
				t.Fatalf("unexpected event %q: %s", f.event, f.data)
			}
		case <-deadline:
			return
		}
	}
}

func decodeSnapshotEvent(t *testing.T, f sseFrame) SnapshotResponse {
	t.Helper()
	if f.event != "snapshot" {
		t.Fatalf("event = %q, want snapshot", f.event)
	}
	var body SnapshotResponse
	if err := json.Unmarshal([]byte(f.data), &body); err != nil {
		t.Fatalf("decode snapshot event: %v", err)
	}
	return body
}

func TestStreamSendsSnapshotImmediately(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	resp, frames := openStream(t, srv.URL)
	body := decodeSnapshotEvent(t, nextEvent(t, frames, time.Second))

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if len(body.Snapshot.Targets) != 1 || body.Snapshot.Targets[0].Host != "a.example" {
		t.Fatalf("targets = %+v, want a.example", body.Snapshot.Targets)
	}
}

func TestStreamSendsNothingWhileStatsAreUnchanged(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())
	_, frames := openStream(t, srv.URL)
	nextEvent(t, frames, time.Second)

	expectNoEvent(t, frames, 60*time.Millisecond)
}

func TestStreamSendsNewSnapshotWhenStatsChange(t *testing.T) {
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())
	_, frames := openStream(t, srv.URL)
	nextEvent(t, frames, time.Second)

	ts.IncSent()
	ts.OnSuccess(7*time.Millisecond, 64)
	body := decodeSnapshotEvent(t, nextEvent(t, frames, time.Second))

	if got := body.Snapshot.Targets[0].Recv; got != 1 {
		t.Fatalf("recv = %d, want 1 after a reply", got)
	}
}

func TestStreamSendsSnapshotWhenReloadStateChanges(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())
	_, frames := openStream(t, srv.URL)
	nextEvent(t, frames, time.Second)

	src.MarkReloading()
	body := decodeSnapshotEvent(t, nextEvent(t, frames, time.Second))

	if !body.Reloading {
		t.Fatal("reloading = false, want true after MarkReloading")
	}
}

func TestStreamWaitsForProviderThenSendsSnapshot(t *testing.T) {
	src := NewSource()
	srv := newTestServer(t, src, defaultTestConfig())
	_, frames := openStream(t, srv.URL)
	expectNoEvent(t, frames, 30*time.Millisecond)

	p, _ := providerWithTarget("late.example")
	src.Set(p)
	body := decodeSnapshotEvent(t, nextEvent(t, frames, time.Second))

	if body.Snapshot.Targets[0].Host != "late.example" {
		t.Fatalf("targets = %+v, want late.example", body.Snapshot.Targets)
	}
}

func TestStreamSendsHeartbeatWhileIdle(t *testing.T) {
	src := NewSource()
	cfg := defaultTestConfig()
	cfg.heartbeatInterval = 10 * time.Millisecond
	srv := newTestServer(t, src, cfg)
	_, frames := openStream(t, srv.URL)

	select {
	case f := <-frames:
		if !f.comment {
			t.Fatalf("first frame = %+v, want a heartbeat comment", f)
		}
	case <-time.After(time.Second):
		t.Fatal("no heartbeat within 1s")
	}
}

func TestStreamEndsWhenHandlerContextIsCancelled(t *testing.T) {
	src := NewSource()
	ctx, cancel := context.WithCancel(context.Background())
	srv := newTestServerWithContext(t, ctx, src, defaultTestConfig())
	_, frames := openStream(t, srv.URL)

	cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-frames:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream still open 1s after the server context was cancelled")
		}
	}
}
