package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	for k, v := range extra {
		h[k] = v
	}
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
