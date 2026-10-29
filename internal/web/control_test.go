package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nagayon-935/mping/internal/report"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
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

func newControlServer(t *testing.T) (*controlProvider, *Source, string) {
	t.Helper()
	base, _ := providerWithTarget("a.example")
	p := &controlProvider{fakeProvider: base}
	src := NewSource()
	src.Set(p)
	cfg := defaultTestConfig()
	cfg.token = testToken
	return p, src, newTestServer(t, src, cfg).URL
}

type req struct {
	method, path, body string
	headers            map[string]string
}

func send(t *testing.T, base string, r req) (int, string) {
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
	return resp.StatusCode, string(b)
}

func authed(extra map[string]string) map[string]string {
	h := map[string]string{"X-Mping-Token": testToken, "Content-Type": "application/json"}
	maps.Copy(h, extra)
	return h
}

func TestSessionReportsControlOnlyWithTheRightToken(t *testing.T) {
	_, _, base := newControlServer(t)

	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"no token", nil, false},
		{"wrong token", map[string]string{"X-Mping-Token": "nope"}, false},
		{"right token", map[string]string{"X-Mping-Token": testToken}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := send(t, base, req{method: http.MethodGet, path: "/api/v1/session", headers: tt.headers})

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

func TestSessionReportsNoControlWhenProviderCannotControl(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	cfg := defaultTestConfig()
	cfg.token = testToken
	base := newTestServer(t, src, cfg).URL

	_, body := send(t, base, req{method: http.MethodGet, path: "/api/v1/session", headers: map[string]string{"X-Mping-Token": testToken}})

	if strings.Contains(body, `"control":true`) {
		t.Fatalf("body = %s, want control=false for a read-only provider", body)
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

func TestAddHostValidatesInput(t *testing.T) {
	p, _, base := newControlServer(t)

	tests := []struct {
		name, body string
		want       int
	}{
		{"malformed json", `{"host":`, http.StatusBadRequest},
		{"unknown field", `{"host":"a.example","extra":1}`, http.StatusBadRequest},
		{"empty", `{"host":"   "}`, http.StatusBadRequest},
		{"inner whitespace", `{"host":"a b"}`, http.StatusBadRequest},
		{"control character", `{"host":"a\u0000b"}`, http.StatusBadRequest},
		{"too long", fmt.Sprintf(`{"host":%q}`, strings.Repeat("a", maxHostLen+1)), http.StatusBadRequest},
		{"trailing data", `{"host":"a.example"} {"host":"b.example"}`, http.StatusBadRequest},
		{"byte order mark", "{\"host\":\"a\ufeffb\"}", http.StatusBadRequest},
		{"body too large", fmt.Sprintf(`{"host":"a.example","pad":%q}`, strings.Repeat("x", maxControlBody)), http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := send(t, base, req{method: http.MethodPost, path: "/api/v1/targets", body: tt.body, headers: authed(nil)})

			if status != tt.want {
				t.Fatalf("status = %d, want %d", status, tt.want)
			}
		})
	}
	if len(p.added) != 0 {
		t.Fatalf("added = %v, want none", p.added)
	}
}

func TestDeleteTargetRejectsNonNumericID(t *testing.T) {
	_, _, base := newControlServer(t)

	status, _ := send(t, base, req{method: http.MethodDelete, path: "/api/v1/targets/abc", headers: authed(nil)})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestControllerErrorsAreReportedAsConflict(t *testing.T) {
	p, _, base := newControlServer(t)
	p.err = errors.New(`host "a.example" is already in the list`)

	status, body := send(t, base, req{method: http.MethodPost, path: "/api/v1/targets", body: `{"host":"a.example"}`, headers: authed(nil)})

	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if !strings.Contains(body, "already in the list") {
		t.Fatalf("body = %s, want the controller's message", body)
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

func TestControlIsUnavailableForReadOnlyProvider(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)
	cfg := defaultTestConfig()
	cfg.token = testToken
	base := newTestServer(t, src, cfg).URL

	status, _ := send(t, base, req{method: http.MethodPost, path: "/api/v1/reset", headers: authed(nil)})

	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
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
	a, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := Start(Options{Port: 0, Source: NewSource()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

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

func TestMonitoringControlsAuthorizationStopAndDownloads(t *testing.T) {
	baseProvider, _ := providerWithTarget("router")
	p := &monitoringProvider{controlProvider: &controlProvider{fakeProvider: baseProvider}}
	src := NewSource()
	src.Set(p)
	cfg := defaultTestConfig()
	cfg.token = testToken
	base := newTestServer(t, src, cfg).URL
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/stop"}, {"POST", "/api/v1/restart"}, {"GET", "/api/v1/report?format=json"},
	} {
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
	if status, _ := send(t, base, req{method: "POST", path: "/api/v1/stop", headers: authed(nil)}); status != 204 || !p.stopped {
		t.Fatalf("stop=%d", status)
	}
	if status, _ := send(t, base, req{method: "POST", path: "/api/v1/restart", headers: authed(nil)}); status != 204 || p.stopped {
		t.Fatalf("restart=%d", status)
	}
	for _, format := range []string{"json", "text"} {
		request, _ := http.NewRequest("GET", base+"/api/v1/report?format="+format, nil)
		request.Header.Set(tokenHeader, testToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Disposition"), "attachment") || len(data) == 0 {
			t.Fatalf("report %s: %d %s", format, response.StatusCode, data)
		}
	}
	if status, _ := send(t, base, req{method: "GET", path: "/api/v1/report?format=csv", headers: authed(nil)}); status != 400 {
		t.Fatalf("invalid format=%d", status)
	}
	src.MarkReloading()
	if status, _ := send(t, base, req{method: "POST", path: "/api/v1/stop", headers: authed(nil)}); status != 409 {
		t.Fatalf("reload control=%d", status)
	}
}

func newMonitoringServer(t *testing.T, p Provider) string {
	t.Helper()
	src := NewSource()
	src.Set(p)
	cfg := defaultTestConfig()
	cfg.token = testToken
	return newTestServer(t, src, cfg).URL
}

func TestMeasurementAndReportControlsNeedACapableProvider(t *testing.T) {
	base, _ := providerWithTarget("router")
	url := newMonitoringServer(t, &controlProvider{fakeProvider: base})

	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/stop"}, {"POST", "/api/v1/restart"}, {"GET", "/api/v1/report"},
	} {
		status, body := send(t, url, req{method: route.method, path: route.path, headers: authed(nil)})

		if status != http.StatusNotImplemented || !strings.Contains(body, "unavailable") {
			t.Errorf("%s %s = %d %s, want 501 unavailable", route.method, route.path, status, body)
		}
	}
}

func TestMeasurementAndReportErrorsAreReportedAsConflict(t *testing.T) {
	base, _ := providerWithTarget("router")
	p := &monitoringProvider{controlProvider: &controlProvider{fakeProvider: base, err: errors.New("supervisor is busy")}}
	url := newMonitoringServer(t, p)

	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/stop"}, {"POST", "/api/v1/restart"}, {"GET", "/api/v1/report"},
	} {
		status, body := send(t, url, req{method: route.method, path: route.path, headers: authed(nil)})

		if status != http.StatusConflict || !strings.Contains(body, "supervisor is busy") {
			t.Errorf("%s %s = %d %s, want 409 with the provider's message", route.method, route.path, status, body)
		}
	}
}

func TestReportDefaultsToJSON(t *testing.T) {
	base, _ := providerWithTarget("router")
	url := newMonitoringServer(t, &monitoringProvider{controlProvider: &controlProvider{fakeProvider: base}})

	status, body := send(t, url, req{method: "GET", path: "/api/v1/report", headers: authed(nil)})

	var decoded map[string]any
	if status != http.StatusOK || json.Unmarshal([]byte(body), &decoded) != nil {
		t.Fatalf("report without format = %d %s, want a JSON report", status, body)
	}
}

func TestDeleteTargetErrorsAreReportedAsConflict(t *testing.T) {
	p, _, base := newControlServer(t)
	p.err = errors.New("no such target")

	status, body := send(t, base, req{method: http.MethodDelete, path: "/api/v1/targets/7", headers: authed(nil)})

	if status != http.StatusConflict || !strings.Contains(body, "no such target") {
		t.Fatalf("delete = %d %s, want 409 with the controller's message", status, body)
	}
}
