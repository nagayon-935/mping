package pinger

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// statusServer serves a fixed status code.
func statusServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// awaitHTTPCheck blocks until r has recorded at least one check outcome.
func awaitHTTPCheck(t *testing.T, r *stats.HTTPCheckResult) {
	t.Helper()
	eventually(t, 5*time.Second, "first HTTP check outcome for "+r.URL(), func() bool {
		v := r.GetView()
		return v.UpCount+v.DownCount >= 1
	})
}

// checkOnce runs a checker (one-hour interval, so exactly one check per URL)
// until every URL has an outcome, then stops it and returns the results.
func checkOnce(t *testing.T, timeout time.Duration, urls ...string) []*stats.HTTPCheckResult {
	t.Helper()
	hc := NewHTTPChecker(urls, time.Hour, timeout, BindConfig{})
	hc.Start()
	for _, r := range hc.Results() {
		awaitHTTPCheck(t, r)
	}
	hc.Stop()
	hc.Wait()
	return hc.Results()
}

func TestHTTPChecker_ClassifiesResponses(t *testing.T) {
	redirectTarget := statusServer(t, http.StatusOK)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer redirect.Close()
	// Redirects forever, so the checker has to give up after maxRedirects.
	endless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := 0
		fmt.Sscanf(r.URL.Query().Get("c"), "%d", &count)
		if count < maxRedirects+1 {
			http.Redirect(w, r, fmt.Sprintf("/?c=%d", count+1), http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer endless.Close()

	tests := []struct {
		name       string
		url        string
		wantStatus string
		wantCode   int
		wantUp     bool // UpCount >= 1 (otherwise DownCount >= 1)
	}{
		{"200 is Up", statusServer(t, http.StatusOK).URL, "Up", http.StatusOK, true},
		{"503 is Down", statusServer(t, http.StatusServiceUnavailable).URL, "Down", http.StatusServiceUnavailable, false},
		{"redirects are followed", redirect.URL, "Up", http.StatusOK, true},
		// The last redirect response is reported once maxRedirects is exceeded.
		{"redirect loop stops at the last redirect", endless.URL, "Up", http.StatusFound, true},
		// Port 1 is privileged and not listening: the connection fails.
		{"connection failure is an Error", "http://127.0.0.1:1", "Error", 0, false},
		{"invalid URL is an Error", "http://[::1", "Error", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := checkOnce(t, 5*time.Second, tt.url)[0].GetView()

			if v.Status != tt.wantStatus || v.StatusCode != tt.wantCode {
				t.Fatalf("Status=%q StatusCode=%d, want %q / %d", v.Status, v.StatusCode, tt.wantStatus, tt.wantCode)
			}
			if tt.wantUp && (v.UpCount < 1 || v.RTT <= 0) {
				t.Errorf("UpCount=%d RTT=%v, want a counted success with a positive RTT", v.UpCount, v.RTT)
			}
			if !tt.wantUp && v.DownCount < 1 {
				t.Errorf("DownCount=%d, want >= 1", v.DownCount)
			}
		})
	}
}

func TestHTTPChecker_MultipleURLs(t *testing.T) {
	ok := statusServer(t, http.StatusOK)
	fail := statusServer(t, http.StatusInternalServerError)

	results := checkOnce(t, 5*time.Second, ok.URL, fail.URL)

	if len(results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(results))
	}
	if v := results[0].GetView(); v.Status != "Up" {
		t.Errorf("[0] Status = %q, want Up", v.Status)
	}
	if v := results[1].GetView(); v.Status != "Down" {
		t.Errorf("[1] Status = %q, want Down", v.Status)
	}
}

func TestHTTPChecker_StopPreventsFurtherChecks(t *testing.T) {
	var mu sync.Mutex
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	calls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return callCount
	}

	hc := NewHTTPChecker([]string{srv.URL}, 20*time.Millisecond, 5*time.Second, BindConfig{})
	hc.Start()
	eventually(t, 5*time.Second, "the checker polled more than once", func() bool { return calls() >= 2 })
	hc.Stop()
	hc.Wait() // once this returns, the loop goroutine cannot make further requests

	countAtStop := calls()
	time.Sleep(100 * time.Millisecond) // several intervals' worth of chances to misbehave
	if final := calls(); final != countAtStop {
		t.Errorf("callCount changed after Stop+Wait: %d → %d (loop goroutine outlived Wait)", countAtStop, final)
	}
}

// TestHTTPChecker_Wait verifies that Wait() actually joins the per-URL loop
// goroutines spawned by Start(), rather than just cancelling their context.
func TestHTTPChecker_Wait(t *testing.T) {
	srv := statusServer(t, http.StatusOK)

	hc := NewHTTPChecker([]string{srv.URL}, 5*time.Millisecond, 5*time.Second, BindConfig{})
	hc.Start()
	awaitHTTPCheck(t, hc.Results()[0])
	hc.Stop()

	done := make(chan struct{})
	go func() {
		hc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return within timeout after Stop()")
	}

	before := hc.Results()[0].GetView().UpCount
	time.Sleep(50 * time.Millisecond) // ten intervals' worth of chances to misbehave
	if after := hc.Results()[0].GetView().UpCount; before != after {
		t.Errorf("UpCount changed after Wait() returned: %d -> %d (loop goroutine outlived Wait)", before, after)
	}
}

// TestHTTPChecker_CheckAfterStopRecordsNothing: a check that starts after
// shutdown must not overwrite the last measurement.
func TestHTTPChecker_CheckAfterStopRecordsNothing(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	hc := NewHTTPChecker([]string{srv.URL}, time.Hour, 5*time.Second, BindConfig{})
	r := hc.Results()[0]
	hc.Stop()

	hc.check(r)

	if v := r.GetView(); v.UpCount+v.DownCount != 0 || v.Status != "Checking..." {
		t.Fatalf("a check after Stop changed the result: %+v", v)
	}
}
