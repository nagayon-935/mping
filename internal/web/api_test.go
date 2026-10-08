package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode %T: %v", v, err)
	}
	return v
}

func TestSnapshotReturns503BeforeProviderIsSet(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	resp := get(t, srv.URL+"/api/v1/snapshot")

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSnapshotReturnsTargetsMetaAndState(t *testing.T) {
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	ts.IncSent()
	ts.OnSuccess(12*time.Millisecond, 57)
	src.Set(p)
	src.MarkReloading()
	srv := newTestServer(t, src, defaultTestConfig())

	resp := get(t, srv.URL+"/api/v1/snapshot")
	body := decode[SnapshotResponse](t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if body.State != StateReloading {
		t.Errorf("state = %q, want %q", body.State, StateReloading)
	}
	if len(body.Snapshot.Targets) != 1 || body.Snapshot.Targets[0].Host != "a.example" {
		t.Fatalf("targets = %+v, want one a.example target", body.Snapshot.Targets)
	}
	if got := body.Snapshot.Targets[0].LastRTTMs; got != 12 {
		t.Errorf("last_rtt_ms = %v, want 12", got)
	}
	if !body.Meta.Features.MTR || body.Meta.IntervalMs != 1000 {
		t.Errorf("meta = %+v, want MTR feature and 1000ms interval", body.Meta)
	}
	if len(body.Meta.Groups) != 1 || body.Meta.Groups[0].TargetIDs[0] != ts.ID {
		t.Errorf("groups = %+v, want core group holding target %d", body.Meta.Groups, ts.ID)
	}
}

func TestSnapshotIncludesHTTPChecks(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	hc := stats.NewHTTPCheckResult("https://example.com/")
	hc.SetResult(200, 30*time.Millisecond, nil)
	p.http = []*stats.HTTPCheckResult{hc}
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	body := decode[SnapshotResponse](t, get(t, srv.URL+"/api/v1/snapshot"))

	if len(body.Snapshot.HTTPChecks) != 1 || body.Snapshot.HTTPChecks[0].URL != "https://example.com/" {
		t.Fatalf("http_checks = %+v, want the example.com check", body.Snapshot.HTTPChecks)
	}
}

func TestHistoryReturnsRTTSeriesWithNullForLoss(t *testing.T) {
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	ts.OnSuccess(10*time.Millisecond, 64)
	ts.OnFailure("timeout")
	ts.OnSuccess(20*time.Millisecond, 64)
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	resp := get(t, fmt.Sprintf("%s/api/v1/targets/%d/history", srv.URL, ts.ID))
	body := decode[HistoryResponse](t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body.ID != ts.ID {
		t.Errorf("id = %d, want %d", body.ID, ts.ID)
	}
	if len(body.RTTMs) != 3 {
		t.Fatalf("rtt_ms = %v, want 3 points", body.RTTMs)
	}
	if body.RTTMs[0] == nil || *body.RTTMs[0] != 10 {
		t.Errorf("rtt_ms[0] = %v, want 10", body.RTTMs[0])
	}
	if body.RTTMs[1] != nil {
		t.Errorf("rtt_ms[1] = %v, want null for a lost probe", *body.RTTMs[1])
	}
	if body.RTTMs[2] == nil || *body.RTTMs[2] != 20 {
		t.Errorf("rtt_ms[2] = %v, want 20", body.RTTMs[2])
	}
}

func TestHistoryLimitsToTrailingNPoints(t *testing.T) {
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	for i := 1; i <= 5; i++ {
		ts.OnSuccess(time.Duration(i)*time.Millisecond, 64)
	}
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	body := decode[HistoryResponse](t, get(t, fmt.Sprintf("%s/api/v1/targets/%d/history?n=2", srv.URL, ts.ID)))

	if len(body.RTTMs) != 2 || *body.RTTMs[0] != 4 || *body.RTTMs[1] != 5 {
		t.Fatalf("rtt_ms = %v, want the trailing [4 5]", body.RTTMs)
	}
}

func TestTargetEndpointsRejectBadInput(t *testing.T) {
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	tests := []struct {
		name string
		path string
		want int
	}{
		{"non-numeric id", "/api/v1/targets/abc/history", http.StatusBadRequest},
		{"unknown id", "/api/v1/targets/999999999/history", http.StatusNotFound},
		{"non-numeric n", fmt.Sprintf("/api/v1/targets/%d/history?n=x", ts.ID), http.StatusBadRequest},
		{"zero n", fmt.Sprintf("/api/v1/targets/%d/history?n=0", ts.ID), http.StatusBadRequest},
		{"n above cap", fmt.Sprintf("/api/v1/targets/%d/history?n=%d", ts.ID, maxHistoryPoints+1), http.StatusBadRequest},
		{"events unknown id", "/api/v1/targets/999999999/events", http.StatusNotFound},
		{"events non-numeric id", "/api/v1/targets/abc/events", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := get(t, srv.URL+tt.path)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestTargetEndpointsReturn503BeforeProviderIsSet(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	for _, path := range []string{"/api/v1/targets/1/history", "/api/v1/targets/1/events"} {
		resp := get(t, srv.URL+path)

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, resp.StatusCode)
		}
	}
}

func TestEventsReturnsTargetEvents(t *testing.T) {
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	ts.RecordEvent("route", "hop 3 changed")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	resp := get(t, fmt.Sprintf("%s/api/v1/targets/%d/events", srv.URL, ts.ID))
	body := decode[EventsResponse](t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if n := len(body.Events); n == 0 || body.Events[n-1].Message != "hop 3 changed" {
		t.Fatalf("events = %+v, want the recorded route event last", body.Events)
	}
}

func TestAPIRejectsNonGETMethods(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	resp, err := http.Post(srv.URL+"/api/v1/snapshot", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestIndexServesHTML(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	resp := get(t, srv.URL+"/")
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(string(body), "<title>mping</title>") {
		t.Errorf("body does not look like the mping index page")
	}
}

func TestUnknownPathReturns404(t *testing.T) {
	srv := newTestServer(t, NewSource(), defaultTestConfig())

	resp := get(t, srv.URL+"/api/v1/nope")

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// countingProvider counts Targets calls so tests can tell whether a
// snapshot was rebuilt or served from cache.
type countingProvider struct {
	*fakeProvider
	calls atomic.Int64
}

func (c *countingProvider) Targets() []*stats.TargetStats {
	c.calls.Add(1)
	return c.fakeProvider.Targets()
}

func TestSnapshotIsReusedUntilGenerationOrSourceChanges(t *testing.T) {
	src := NewSource()
	base, _ := providerWithTarget("a.example")
	p := &countingProvider{fakeProvider: base}
	src.Set(p)
	var gen atomic.Uint64
	cfg := defaultTestConfig()
	cfg.generation = gen.Load
	srv := newTestServer(t, src, cfg)

	get(t, srv.URL+"/api/v1/snapshot")
	get(t, srv.URL+"/api/v1/snapshot")
	unchanged := p.calls.Load()
	gen.Add(1)
	get(t, srv.URL+"/api/v1/snapshot")
	afterGen := p.calls.Load()
	src.MarkReloading()
	reloaded := decode[SnapshotResponse](t, get(t, srv.URL+"/api/v1/snapshot"))

	if unchanged != 1 {
		t.Errorf("Targets calls after two unchanged requests = %d, want 1", unchanged)
	}
	if afterGen != 2 {
		t.Errorf("Targets calls after a generation bump = %d, want 2", afterGen)
	}
	if reloaded.State != StateReloading {
		t.Errorf("state after MarkReloading = %q, want %q", reloaded.State, StateReloading)
	}
}
