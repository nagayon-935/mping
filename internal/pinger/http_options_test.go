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
