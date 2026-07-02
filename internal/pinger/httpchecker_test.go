package pinger

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPChecker_Up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hc := NewHTTPChecker([]string{srv.URL}, time.Hour, 5*time.Second)
	hc.Start()
	time.Sleep(200 * time.Millisecond)
	hc.Stop()

	v := hc.Results()[0].GetView()
	if v.Status != "Up" {
		t.Errorf("Status = %q, want Up", v.Status)
	}
	if v.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", v.StatusCode)
	}
	if v.UpCount < 1 {
		t.Errorf("UpCount = %d, want >= 1", v.UpCount)
	}
	if v.RTT <= 0 {
		t.Errorf("RTT = %v, want > 0", v.RTT)
	}
}

func TestHTTPChecker_Down(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	hc := NewHTTPChecker([]string{srv.URL}, time.Hour, 5*time.Second)
	hc.Start()
	time.Sleep(200 * time.Millisecond)
	hc.Stop()

	v := hc.Results()[0].GetView()
	if v.Status != "Down" {
		t.Errorf("Status = %q, want Down", v.Status)
	}
	if v.DownCount < 1 {
		t.Errorf("DownCount = %d, want >= 1", v.DownCount)
	}
}

func TestHTTPChecker_Error(t *testing.T) {
	// Port 1 is privileged and not listening: connection should fail.
	hc := NewHTTPChecker([]string{"http://127.0.0.1:1"}, time.Hour, 200*time.Millisecond)
	hc.Start()
	time.Sleep(500 * time.Millisecond)
	hc.Stop()

	v := hc.Results()[0].GetView()
	if v.Status != "Error" {
		t.Errorf("Status = %q, want Error", v.Status)
	}
	if v.DownCount < 1 {
		t.Errorf("DownCount = %d, want >= 1", v.DownCount)
	}
}

func TestHTTPChecker_MultipleURLs(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fail.Close()

	hc := NewHTTPChecker([]string{ok.URL, fail.URL}, time.Hour, 5*time.Second)
	hc.Start()
	time.Sleep(200 * time.Millisecond)
	hc.Stop()

	if len(hc.Results()) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(hc.Results()))
	}
	if v := hc.Results()[0].GetView(); v.Status != "Up" {
		t.Errorf("[0] Status = %q, want Up", v.Status)
	}
	if v := hc.Results()[1].GetView(); v.Status != "Down" {
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

	hc := NewHTTPChecker([]string{srv.URL}, 50*time.Millisecond, 5*time.Second)
	hc.Start()
	time.Sleep(120 * time.Millisecond)
	hc.Stop()
	hc.Wait() // once this returns, the loop goroutine cannot make further requests

	mu.Lock()
	countAtStop := callCount
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	final := callCount
	mu.Unlock()
	if final != countAtStop {
		t.Errorf("callCount changed after Stop+Wait: %d → %d (loop goroutine outlived Wait)", countAtStop, final)
	}
}

// TestHTTPChecker_Wait verifies that Wait() actually joins the per-URL loop
// goroutines spawned by Start(), rather than just cancelling their context.
func TestHTTPChecker_Wait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hc := NewHTTPChecker([]string{srv.URL}, 5*time.Millisecond, 5*time.Second)
	hc.Start()
	time.Sleep(20 * time.Millisecond) // let the loop run at least once
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
	time.Sleep(50 * time.Millisecond)
	after := hc.Results()[0].GetView().UpCount
	if before != after {
		t.Errorf("UpCount changed after Wait() returned: %d -> %d (loop goroutine outlived Wait)", before, after)
	}
}

func TestHTTPChecker_FollowsRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	hc := NewHTTPChecker([]string{redirect.URL}, time.Hour, 5*time.Second)
	hc.Start()
	time.Sleep(200 * time.Millisecond)
	hc.Stop()

	v := hc.Results()[0].GetView()
	if v.Status != "Up" {
		t.Errorf("Status = %q, want Up (redirect should be followed)", v.Status)
	}
}
