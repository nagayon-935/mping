package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	if len(body.Meta.Groups) != 1 || body.Meta.Groups[0].TargetIDs[0] != ts.ID() {
		t.Errorf("groups = %+v, want core group holding target %d", body.Meta.Groups, ts.ID())
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

	resp := get(t, fmt.Sprintf("%s/api/v1/targets/%d/history", srv.URL, ts.ID()))
	body := decode[HistoryResponse](t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body.ID != ts.ID() {
		t.Errorf("id = %d, want %d", body.ID, ts.ID())
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

	body := decode[HistoryResponse](t, get(t, fmt.Sprintf("%s/api/v1/targets/%d/history?n=2", srv.URL, ts.ID())))

	if len(body.RTTMs) != 2 || *body.RTTMs[0] != 4 || *body.RTTMs[1] != 5 {
		t.Fatalf("rtt_ms = %v, want the trailing [4 5]", body.RTTMs)
	}
}

// TestEndpointsRejectBadInputAndMissingProvider covers request validation
// of every read endpoint, and the 503 each returns before a provider is set.
// {id} stands for the id of the one target the provider holds.
func TestEndpointsRejectBadInputAndMissingProvider(t *testing.T) {
	empty := newTestServer(t, NewSource(), defaultTestConfig())
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	src.Set(p)
	loaded := newTestServer(t, src, defaultTestConfig())

	tests := []struct {
		name string
		srv  *httptest.Server
		path string
		want int
	}{
		{"snapshot without provider", empty, "/api/v1/snapshot", http.StatusServiceUnavailable},
		{"target history without provider", empty, "/api/v1/targets/1/history", http.StatusServiceUnavailable},
		{"target events without provider", empty, "/api/v1/targets/1/events", http.StatusServiceUnavailable},
		{"bulk history without provider", empty, "/api/v1/history", http.StatusServiceUnavailable},
		{"bulk events without provider", empty, "/api/v1/events", http.StatusServiceUnavailable},
		{"history non-numeric id", loaded, "/api/v1/targets/abc/history", http.StatusBadRequest},
		{"history unknown id", loaded, "/api/v1/targets/999999999/history", http.StatusNotFound},
		{"history non-numeric n", loaded, "/api/v1/targets/{id}/history?n=x", http.StatusBadRequest},
		{"history zero n", loaded, "/api/v1/targets/{id}/history?n=0", http.StatusBadRequest},
		{"history n above cap", loaded, fmt.Sprintf("/api/v1/targets/{id}/history?n=%d", maxHistoryPoints+1), http.StatusBadRequest},
		{"events unknown id", loaded, "/api/v1/targets/999999999/events", http.StatusNotFound},
		{"events non-numeric id", loaded, "/api/v1/targets/abc/events", http.StatusBadRequest},
		{"bulk history default n", loaded, "/api/v1/history", http.StatusOK},
		{"bulk history bad n", loaded, "/api/v1/history?n=abc", http.StatusBadRequest},
		{"bulk history n above cap", loaded, fmt.Sprintf("/api/v1/history?n=%d", maxHistoryPoints+1), http.StatusBadRequest},
		{"bulk events default limit", loaded, "/api/v1/events", http.StatusOK},
		{"bulk events bad limit", loaded, "/api/v1/events?limit=x", http.StatusBadRequest},
		{"bulk events zero limit", loaded, "/api/v1/events?limit=0", http.StatusBadRequest},
		{"bulk events limit above cap", loaded, fmt.Sprintf("/api/v1/events?limit=%d", maxEvents+1), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := strings.ReplaceAll(tt.path, "{id}", fmt.Sprint(ts.ID()))

			resp := get(t, tt.srv.URL+path)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestEventsReturnsTargetEvents(t *testing.T) {
	src := NewSource()
	p, ts := providerWithTarget("a.example")
	ts.RecordEvent("route", "hop 3 changed")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	resp := get(t, fmt.Sprintf("%s/api/v1/targets/%d/events", srv.URL, ts.ID()))
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

func TestSnapshotIsReusedUntilGenerationOrSourceChanges(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	var gen atomic.Uint64
	cfg := defaultTestConfig()
	cfg.generation = gen.Load
	srv := newTestServer(t, src, cfg)

	get(t, srv.URL+"/api/v1/snapshot")
	get(t, srv.URL+"/api/v1/snapshot")
	unchanged := p.targetCalls.Load()
	gen.Add(1)
	get(t, srv.URL+"/api/v1/snapshot")
	afterGen := p.targetCalls.Load()
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

func TestBulkHistoryReturnsEveryTargetsTrailingSeries(t *testing.T) {
	src := NewSource()
	p, a := providerWithTarget("a.example")
	b := stats.NewTargetStats("b.example")
	p.targets = append(p.targets, b)
	for i := 1; i <= 3; i++ {
		a.OnSuccess(time.Duration(i)*time.Millisecond, 64)
	}
	b.OnFailure("timeout")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	resp := get(t, srv.URL+"/api/v1/history?n=2")
	body := decode[BulkHistoryResponse](t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(body.Targets) != 2 {
		t.Fatalf("targets = %+v, want 2 series", body.Targets)
	}
	if got := body.Targets[0]; got.ID != a.ID() || len(got.RTTMs) != 2 || *got.RTTMs[0] != 2 || *got.RTTMs[1] != 3 {
		t.Errorf("a series = %+v, want id %d with [2 3]", got, a.ID())
	}
	if got := body.Targets[1]; got.ID != b.ID() || len(got.RTTMs) != 1 || got.RTTMs[0] != nil {
		t.Errorf("b series = %+v, want id %d with [null]", got, b.ID())
	}
}

func TestBulkEventsMergesTargetsNewestFirstWithHosts(t *testing.T) {
	src := NewSource()
	p, a := providerWithTarget("a.example") // SetIP records a "dns" event on a
	b := stats.NewTargetStats("b.example")
	p.targets = append(p.targets, b)
	// The sleeps give each event a distinct timestamp to sort by.
	a.RecordEvent("route", "a first")
	time.Sleep(2 * time.Millisecond)
	b.RecordEvent("port", "b second")
	time.Sleep(2 * time.Millisecond)
	a.RecordEvent("ping", "a third")
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	resp := get(t, srv.URL+"/api/v1/events")
	body := decode[BulkEventsResponse](t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(body.Events) != 4 {
		t.Fatalf("events = %+v, want 4", body.Events)
	}
	want := []struct{ host, msg string }{{"a.example", "a third"}, {"b.example", "b second"}, {"a.example", "a first"}}
	for i, w := range want {
		if got := body.Events[i]; got.Host != w.host || got.Message != w.msg {
			t.Errorf("events[%d] = %s %q, want %s %q", i, got.Host, got.Message, w.host, w.msg)
		}
	}
	if got := body.Events[1].TargetID; got != b.ID() {
		t.Errorf("events[1].target_id = %d, want %d", got, b.ID())
	}
}

func TestBulkEventsLimitKeepsTheNewest(t *testing.T) {
	src := NewSource()
	p, a := providerWithTarget("a.example")
	for i := range 5 {
		time.Sleep(time.Millisecond)
		a.RecordEvent("ping", fmt.Sprintf("event %d", i))
	}
	src.Set(p)
	srv := newTestServer(t, src, defaultTestConfig())

	body := decode[BulkEventsResponse](t, get(t, srv.URL+"/api/v1/events?limit=2"))

	if len(body.Events) != 2 || body.Events[0].Message != "event 4" || body.Events[1].Message != "event 3" {
		t.Fatalf("events = %+v, want [event 4, event 3]", body.Events)
	}
}

func TestBulkEventsIsAnEmptyListWithoutEvents(t *testing.T) {
	src := NewSource()
	src.Set(&fakeProvider{targets: []*stats.TargetStats{stats.NewTargetStats("quiet.example")}})
	srv := newTestServer(t, src, defaultTestConfig())

	resp := get(t, srv.URL+"/api/v1/events")
	raw, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(raw), `"events":[]`) {
		t.Fatalf("body = %s, want an empty events array (not null)", raw)
	}
}

func TestHistoryIncludesActualSampleTimesAndNullLoss(t *testing.T) {
	p, target := providerWithTarget("router")
	before := time.Now()
	target.OnSuccess(1500*time.Microsecond, 64)
	target.OnFailure("timeout")
	src := NewSource()
	src.Set(p)
	base := newTestServer(t, src, defaultTestConfig()).URL
	body := decode[HistoryResponse](t, get(t, fmt.Sprintf("%s/api/v1/targets/%d/history?n=2", base, target.ID())))
	if len(body.Samples) != 2 || len(body.RTTMs) != 2 || body.Samples[0].RTTMS == nil || *body.Samples[0].RTTMS != 1.5 || body.Samples[1].RTTMS != nil {
		t.Fatalf("samples=%+v", body.Samples)
	}
	if body.Samples[0].At.Before(before) || body.CapturedAt.Before(body.Samples[1].At) {
		t.Fatal("history timestamps were synthesized incorrectly")
	}
}
