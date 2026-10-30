package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/nagayon-935/mping/internal/report"
)

const testToken = "test-token-0123456789abcdef"

// controlProvider is a fakeProvider that also implements Controller and
// records what it was asked to do.
type controlProvider struct {
	*fakeProvider
	mu      sync.Mutex
	added   []string
	deleted []uint64
	resets  int
	err     error
}

func (c *controlProvider) AddHost(host string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.added = append(c.added, host)
	return nil
}

func (c *controlProvider) DeleteTarget(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.deleted = append(c.deleted, id)
	return nil
}

func (c *controlProvider) ResetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resets++
}

// newTokenServer serves p with the control token configured.
func newTokenServer(t *testing.T, p Provider) (*Source, string) {
	t.Helper()
	src := NewSource()
	src.Set(p)
	cfg := defaultTestConfig()
	cfg.token = testToken
	return src, newTestServer(t, src, cfg).URL
}

func newControlServer(t *testing.T) (*controlProvider, *Source, string) {
	t.Helper()
	base, _ := providerWithTarget("a.example")
	p := &controlProvider{fakeProvider: base}
	src, url := newTokenServer(t, p)
	return p, src, url
}

// newMonitoringServer serves a provider that also supports stop, restart
// and reports.
func newMonitoringServer(t *testing.T, err error) (*monitoringProvider, *Source, string) {
	t.Helper()
	base, _ := providerWithTarget("router")
	p := &monitoringProvider{controlProvider: &controlProvider{fakeProvider: base, err: err}}
	src, url := newTokenServer(t, p)
	return p, src, url
}

type req struct {
	method, path, body string
	headers            map[string]string
}

func send(t *testing.T, base string, r req) (int, string) {
	t.Helper()
	status, _, body := do(t, base, r)
	return status, body
}

func do(t *testing.T, base string, r req) (int, http.Header, string) {
	t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	hr, err := http.NewRequest(r.method, base+r.path, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatalf("%s %s: %v", r.method, r.path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func authed(extra map[string]string) map[string]string {
	h := map[string]string{"X-Mping-Token": testToken, "Content-Type": "application/json"}
	maps.Copy(h, extra)
	return h
}

func TestSessionReportsControlOnlyWithTheRightToken(t *testing.T) {
	_, _, controllable := newControlServer(t)
	readOnlyProvider, _ := providerWithTarget("a.example")
	_, readOnly := newTokenServer(t, readOnlyProvider)
	right := map[string]string{"X-Mping-Token": testToken}

	tests := []struct {
		name    string
		base    string
		headers map[string]string
		want    bool
	}{
		{"no token", controllable, nil, false},
		{"wrong token", controllable, map[string]string{"X-Mping-Token": "nope"}, false},
		{"right token", controllable, right, true},
		{"right token but a provider that cannot control", readOnly, right, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := send(t, tt.base, req{method: http.MethodGet, path: "/api/v1/session", headers: tt.headers})

			var got SessionResponse
			if err := json.Unmarshal([]byte(body), &got); err != nil || status != http.StatusOK {
				t.Fatalf("status %d body %q err %v", status, body, err)
			}
			if got.Control != tt.want {
				t.Fatalf("control = %v, want %v", got.Control, tt.want)
			}
		})
	}
}

func TestAddHostCallsControllerWithTrimmedHost(t *testing.T) {
	p, _, base := newControlServer(t)

	status, _ := send(t, base, req{method: http.MethodPost, path: "/api/v1/targets", body: `{"host":"  new.example "}`, headers: authed(nil)})

	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}
	if len(p.added) != 1 || p.added[0] != "new.example" {
		t.Fatalf("added = %v, want [new.example]", p.added)
	}
}

func TestDeleteTargetCallsControllerWithID(t *testing.T) {
	p, _, base := newControlServer(t)

	status, _ := send(t, base, req{method: http.MethodDelete, path: "/api/v1/targets/42", headers: authed(nil)})

	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	if len(p.deleted) != 1 || p.deleted[0] != 42 {
		t.Fatalf("deleted = %v, want [42]", p.deleted)
	}
}

func TestResetCallsController(t *testing.T) {
	p, _, base := newControlServer(t)

	status, _ := send(t, base, req{method: http.MethodPost, path: "/api/v1/reset", headers: authed(nil)})

	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	if p.resets != 1 {
		t.Fatalf("resets = %d, want 1", p.resets)
	}
}

func TestControlRequestsAreRejectedWithoutAuthority(t *testing.T) {
	p, _, base := newControlServer(t)
	add := func(h map[string]string, body string) req {
		return req{method: http.MethodPost, path: "/api/v1/targets", body: body, headers: h}
	}

	tests := []struct {
		name string
		r    req
		want int
	}{
		{"no token", add(map[string]string{"Content-Type": "application/json"}, `{"host":"x.example"}`), http.StatusForbidden},
		{"wrong token", add(map[string]string{"X-Mping-Token": "nope", "Content-Type": "application/json"}, `{"host":"x.example"}`), http.StatusForbidden},
		{"token via query is not accepted", req{method: http.MethodPost, path: "/api/v1/reset?token=" + testToken}, http.StatusForbidden},
		{"cross-origin", add(authed(map[string]string{"Origin": "https://evil.example"}), `{"host":"x.example"}`), http.StatusForbidden},
		{"form content type", add(authed(map[string]string{"Content-Type": "application/x-www-form-urlencoded"}), `host=x.example`), http.StatusUnsupportedMediaType},
		{"delete without token", req{method: http.MethodDelete, path: "/api/v1/targets/1"}, http.StatusForbidden},
		{"reset without token", req{method: http.MethodPost, path: "/api/v1/reset"}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := send(t, base, tt.r)

			if status != tt.want {
				t.Fatalf("status = %d, want %d", status, tt.want)
			}
		})
	}
	if len(p.added)+len(p.deleted)+p.resets != 0 {
		t.Fatalf("controller was called: added=%v deleted=%v resets=%d", p.added, p.deleted, p.resets)
	}
}

func TestControlInputIsValidated(t *testing.T) {
	p, _, base := newControlServer(t)

	add := func(body string) req {
		return req{method: http.MethodPost, path: "/api/v1/targets", body: body, headers: authed(nil)}
	}
	tests := []struct {
		name string
		r    req
		want int
	}{
		{"delete with a non-numeric id", req{method: http.MethodDelete, path: "/api/v1/targets/abc", headers: authed(nil)}, http.StatusBadRequest},
		{"malformed json", add(`{"host":`), http.StatusBadRequest},
		{"unknown field", add(`{"host":"a.example","extra":1}`), http.StatusBadRequest},
		{"empty", add(`{"host":"   "}`), http.StatusBadRequest},
		{"inner whitespace", add(`{"host":"a b"}`), http.StatusBadRequest},
		{"control character", add(`{"host":"a\u0000b"}`), http.StatusBadRequest},
		{"too long", add(fmt.Sprintf(`{"host":%q}`, strings.Repeat("a", maxHostLen+1))), http.StatusBadRequest},
		{"trailing data", add(`{"host":"a.example"} {"host":"b.example"}`), http.StatusBadRequest},
		{"byte order mark", add("{\"host\":\"a\ufeffb\"}"), http.StatusBadRequest},
		{"body too large", add(fmt.Sprintf(`{"host":"a.example","pad":%q}`, strings.Repeat("x", maxControlBody))), http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := send(t, base, tt.r)

			if status != tt.want {
				t.Fatalf("status = %d, want %d", status, tt.want)
			}
		})
	}
	if len(p.added)+len(p.deleted) != 0 {
		t.Fatalf("controller was called: added=%v deleted=%v", p.added, p.deleted)
	}
}

func TestControlIsUnavailableUnlessRunning(t *testing.T) {
	for _, mark := range []func(*Source){(*Source).MarkReloading, (*Source).MarkStopped} {
		p, src, base := newControlServer(t)
		mark(src)

		status, _ := send(t, base, req{method: http.MethodPost, path: "/api/v1/reset", headers: authed(nil)})

		if status != http.StatusConflict {
			t.Fatalf("state %s: status = %d, want 409", src.State(), status)
		}
		if p.resets != 0 {
			t.Fatalf("state %s: reset ran", src.State())
		}
	}
}

func TestControlIsDisabledWithoutConfiguredToken(t *testing.T) {
	base, _ := providerWithTarget("a.example")
	p := &controlProvider{fakeProvider: base}
	src := NewSource()
	src.Set(p)
	url := newTestServer(t, src, defaultTestConfig()).URL // no token configured

	status, _ := send(t, url, req{method: http.MethodPost, path: "/api/v1/reset", headers: map[string]string{"X-Mping-Token": "", "Content-Type": "application/json"}})

	if status != http.StatusForbidden || p.resets != 0 {
		t.Fatalf("status = %d resets = %d, want 403 and no reset (an empty token must never match)", status, p.resets)
	}
}

func TestStartGeneratesAControlToken(t *testing.T) {
	a, b := startServer(t), startServer(t)

	ua, ub := a.ControlURL(), b.ControlURL()

	if !strings.HasPrefix(ua, a.URL()+"#token=") || len(ua) < len(a.URL())+len("#token=")+32 {
		t.Fatalf("ControlURL = %q, want %s#token=<at least 32 chars>", ua, a.URL())
	}
	if strings.TrimPrefix(ua, a.URL()) == strings.TrimPrefix(ub, b.URL()) {
		t.Fatal("two servers share a token")
	}
}

func TestStartUsesAConfiguredToken(t *testing.T) {
	token := strings.Repeat("k", 32)
	s, err := Start(Options{Port: 0, Source: NewSource(), Token: token})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	got := s.ControlURL()

	if want := s.URL() + "#token=" + token; got != want {
		t.Fatalf("ControlURL = %q, want %q", got, want)
	}
}

func TestStartRejectsWeakOrMalformedTokens(t *testing.T) {
	for name, token := range map[string]string{
		"too short":     strings.Repeat("k", minTokenLen-1),
		"has a space":   strings.Repeat("k", minTokenLen) + " x",
		"has a newline": strings.Repeat("k", minTokenLen) + "\n",
		"has a hash":    strings.Repeat("k", minTokenLen) + "#x",
		"has an amp":    strings.Repeat("k", minTokenLen) + "&x",
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Start(Options{Port: 0, Source: NewSource(), Token: token})

			if err == nil {
				s.Close()
				t.Fatal("Start accepted the token, want an error")
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("error %q echoes the token", err)
			}
		})
	}
}

// Optional controls share the token, origin and source-state boundary with
// target edits, including the GET download endpoint.
type monitoringProvider struct {
	*controlProvider
	stopped bool
	reports int
}

func (p *monitoringProvider) setStopped(stopped bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.stopped = stopped
	return nil
}
func (p *monitoringProvider) StopMeasurements() error    { return p.setStopped(true) }
func (p *monitoringProvider) RestartMeasurements() error { return p.setStopped(false) }
func (p *monitoringProvider) CaptureReport() (report.Report, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reports++
	return report.Report{SchemaVersion: 1, Scope: "session"}, p.err
}

func TestMonitoringControls(t *testing.T) {
	p, src, base := newMonitoringServer(t, nil)
	routes := []struct{ method, path string }{
		{"POST", "/api/v1/stop"}, {"POST", "/api/v1/restart"}, {"GET", "/api/v1/report?format=json"},
	}

	t.Run("unauthorized and cross-origin requests are forbidden", func(t *testing.T) {
		for _, route := range routes {
			if status, _ := send(t, base, req{method: route.method, path: route.path}); status != http.StatusForbidden {
				t.Fatalf("unauthorized %s: %d", route.path, status)
			}
			if status, _ := send(t, base, req{method: route.method, path: route.path, headers: authed(map[string]string{"Origin": "https://elsewhere.example"})}); status != http.StatusForbidden {
				t.Fatalf("cross-origin %s: %d", route.path, status)
			}
		}
		if p.reports != 0 || p.stopped {
			t.Fatal("unauthorized request changed provider")
		}
	})

	t.Run("stop then restart toggle measurements", func(t *testing.T) {
		if status, _ := send(t, base, req{method: "POST", path: "/api/v1/stop", headers: authed(nil)}); status != 204 || !p.stopped {
			t.Fatalf("stop=%d stopped=%v", status, p.stopped)
		}
		if status, _ := send(t, base, req{method: "POST", path: "/api/v1/restart", headers: authed(nil)}); status != 204 || p.stopped {
			t.Fatalf("restart=%d stopped=%v", status, p.stopped)
		}
	})

	t.Run("reports download as attachments", func(t *testing.T) {
		for format, contentType := range map[string]string{"json": "application/json", "csv": "text/csv; charset=utf-8"} {
			status, header, data := do(t, base, req{method: "GET", path: "/api/v1/report?format=" + format, headers: map[string]string{tokenHeader: testToken}})
			if status != 200 || !strings.Contains(header.Get("Content-Disposition"), "mping-report."+format) || len(data) == 0 {
				t.Fatalf("report %s: %d %s", format, status, data)
			}
			if got := header.Get("Content-Type"); !strings.HasPrefix(got, contentType) {
				t.Errorf("report %s Content-Type = %q, want %q", format, got, contentType)
			}
		}
	})

	t.Run("unknown report format is rejected", func(t *testing.T) {
		if status, _ := send(t, base, req{method: "GET", path: "/api/v1/report?format=text", headers: authed(nil)}); status != 400 {
			t.Fatalf("invalid format=%d", status)
		}
	})

	t.Run("controls are a conflict while reloading", func(t *testing.T) {
		src.MarkReloading()
		if status, _ := send(t, base, req{method: "POST", path: "/api/v1/stop", headers: authed(nil)}); status != 409 {
			t.Fatalf("reload control=%d", status)
		}
	})
}

func TestReportDefaultsToJSON(t *testing.T) {
	_, _, url := newMonitoringServer(t, nil)

	status, body := send(t, url, req{method: "GET", path: "/api/v1/report", headers: authed(nil)})

	var decoded map[string]any
	if status != http.StatusOK || json.Unmarshal([]byte(body), &decoded) != nil {
		t.Fatalf("report without format = %d %s, want a JSON report", status, body)
	}
}

// TestControllerErrorsAreReportedAsConflict verifies that an error from the
// provider on any control route becomes a 409 carrying its message.
func TestControllerErrorsAreReportedAsConflict(t *testing.T) {
	_, _, base := newMonitoringServer(t, errors.New("supervisor is busy"))

	tests := []struct {
		name string
		r    req
	}{
		{"add host", req{method: http.MethodPost, path: "/api/v1/targets", body: `{"host":"a.example"}`, headers: authed(nil)}},
		{"delete target", req{method: http.MethodDelete, path: "/api/v1/targets/7", headers: authed(nil)}},
		{"stop", req{method: http.MethodPost, path: "/api/v1/stop", headers: authed(nil)}},
		{"restart", req{method: http.MethodPost, path: "/api/v1/restart", headers: authed(nil)}},
		{"report", req{method: http.MethodGet, path: "/api/v1/report", headers: authed(nil)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := send(t, base, tt.r)

			if status != http.StatusConflict || !strings.Contains(body, "supervisor is busy") {
				t.Fatalf("%s %s = %d %s, want 409 with the provider's message", tt.r.method, tt.r.path, status, body)
			}
		})
	}
}

// TestControlIsUnavailableWithoutTheCapability verifies the 501 for a route
// whose provider does not implement it: reset on a read-only provider, and
// stop/restart/report on one that only edits targets.
func TestControlIsUnavailableWithoutTheCapability(t *testing.T) {
	readOnly, _ := providerWithTarget("a.example")
	_, readOnlyURL := newTokenServer(t, readOnly)
	editBase, _ := providerWithTarget("router")
	_, editURL := newTokenServer(t, &controlProvider{fakeProvider: editBase})

	tests := []struct {
		name string
		base string
		r    req
	}{
		{"reset on a read-only provider", readOnlyURL, req{method: http.MethodPost, path: "/api/v1/reset", headers: authed(nil)}},
		{"stop", editURL, req{method: http.MethodPost, path: "/api/v1/stop", headers: authed(nil)}},
		{"restart", editURL, req{method: http.MethodPost, path: "/api/v1/restart", headers: authed(nil)}},
		{"report", editURL, req{method: http.MethodGet, path: "/api/v1/report", headers: authed(nil)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := send(t, tt.base, tt.r)

			if status != http.StatusNotImplemented {
				t.Fatalf("%s %s = %d %s, want 501", tt.r.method, tt.r.path, status, body)
			}
		})
	}
}
