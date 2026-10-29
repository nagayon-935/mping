package pinger

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPPolicyStatusHeadersRedirectAndCrossOriginSecrets(t *testing.T) {
	secretReceived := make(chan string, 10)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secretReceived <- r.Header.Get("X-API-Key")
		w.WriteHeader(401)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		secretReceived <- r.Header.Get("X-API-Key")
		w.WriteHeader(401)
	}))
	defer origin.Close()
	expected, err := ParseHTTPStatus("200-299,401")
	if err != nil {
		t.Fatal(err)
	}
	opts := HTTPOptions{ExpectedStatus: expected, FollowRedirects: true, Headers: http.Header{"X-Api-Key": []string{"secret"}}}
	hc := NewHTTPCheckerWithOptions([]string{origin.URL}, time.Second, time.Second, BindConfig{}, opts)
	hc.check(hc.results[0])
	hc.Stop()
	if got := hc.results[0].GetView(); got.Status != "Up" || got.StatusCode != 401 {
		t.Fatalf("result=%+v", got)
	}
	if <-secretReceived != "secret" {
		t.Fatal("header missing on origin")
	}
	redirect := NewHTTPCheckerWithOptions([]string{origin.URL + "/redirect"}, time.Second, time.Second, BindConfig{}, opts)
	redirect.check(redirect.results[0])
	redirect.Stop()
	if <-secretReceived != "" {
		t.Fatal("custom secret forwarded to a different origin")
	}
	opts.FollowRedirects = false
	stopped := NewHTTPCheckerWithOptions([]string{origin.URL + "/redirect"}, time.Second, time.Second, BindConfig{}, opts)
	stopped.check(stopped.results[0])
	stopped.Stop()
	if got := stopped.results[0].GetView(); got.StatusCode != 302 || got.Status != "Down" {
		t.Fatalf("redirect policy ignored: %+v", got)
	}
}

// TestHTTPCheckerKeepsItsOwnOptions pins that the checker copies its options:
// changing the caller's HTTPOptions afterwards affects neither the headers it
// sends nor its redirect policy.
func TestHTTPCheckerKeepsItsOwnOptions(t *testing.T) {
	received := make(chan string, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("X-API-Key")
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer origin.Close()

	opts := HTTPOptions{FollowRedirects: true, Headers: http.Header{"X-Api-Key": []string{"secret"}}}
	hc := NewHTTPCheckerWithOptions([]string{origin.URL}, time.Second, time.Second, BindConfig{}, opts)
	defer hc.Stop()
	opts.FollowRedirects = false
	opts.Headers.Del("X-Api-Key") // would also stop the cross-origin strip if shared

	hc.check(hc.results[0])
	select {
	case got := <-received:
		if got != "" {
			t.Fatalf("cross-origin request carried X-API-Key %q", got)
		}
	default:
		t.Fatalf("redirect not followed: %+v", hc.results[0].GetView())
	}
	if got := hc.options.Headers.Get("X-Api-Key"); got != "secret" {
		t.Fatalf("checker headers = %q, want its own copy of %q", got, "secret")
	}
}
