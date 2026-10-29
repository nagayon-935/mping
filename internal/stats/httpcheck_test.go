package stats

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestHTTPCheckResult_InitialStatus(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	v := r.GetView()
	if v.URL != "https://example.com" {
		t.Errorf("URL = %q, want %q", v.URL, "https://example.com")
	}
	if v.Status != "Checking..." {
		t.Errorf("Status = %q, want Checking...", v.Status)
	}
	if v.UpCount != 0 || v.DownCount != 0 {
		t.Errorf("counts should be zero initially, got up=%d down=%d", v.UpCount, v.DownCount)
	}
}

func TestHTTPCheckResult_SetResult(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		rtt        time.Duration
		err        error
		wantStatus string
		wantUp     int
		wantDown   int
		wantMinRTT time.Duration // only checks counted Up feed the RTT stats
	}{
		{name: "2xx is up", code: 200, rtt: 10 * time.Millisecond, wantStatus: "Up", wantUp: 1, wantMinRTT: 10 * time.Millisecond},
		{name: "3xx is up", code: 301, rtt: 5 * time.Millisecond, wantStatus: "Up", wantUp: 1, wantMinRTT: 5 * time.Millisecond},
		{name: "4xx is down", code: 404, rtt: 20 * time.Millisecond, wantStatus: "Down", wantDown: 1},
		{name: "5xx is down", code: 503, rtt: 15 * time.Millisecond, wantStatus: "Down", wantDown: 1},
		{name: "transport error", err: errors.New("connection refused"), wantStatus: "Error", wantDown: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewHTTPCheckResult("https://example.com")

			r.SetResult(tt.code, tt.rtt, tt.err)

			v := r.GetView()
			if v.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", v.Status, tt.wantStatus)
			}
			if v.StatusCode != tt.code {
				t.Errorf("StatusCode = %d, want %d", v.StatusCode, tt.code)
			}
			if v.UpCount != tt.wantUp || v.DownCount != tt.wantDown {
				t.Errorf("UpCount/DownCount = %d/%d, want %d/%d", v.UpCount, v.DownCount, tt.wantUp, tt.wantDown)
			}
			if v.MinRTT != tt.wantMinRTT {
				t.Errorf("MinRTT = %v, want %v", v.MinRTT, tt.wantMinRTT)
			}
		})
	}
}

func TestHTTPCheckResult_RTTStats(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	r.SetResult(200, 10*time.Millisecond, nil)
	r.SetResult(200, 30*time.Millisecond, nil)
	r.SetResult(200, 20*time.Millisecond, nil)
	v := r.GetView()
	if v.MinRTT != 10*time.Millisecond {
		t.Errorf("MinRTT = %v, want 10ms", v.MinRTT)
	}
	if v.MaxRTT != 30*time.Millisecond {
		t.Errorf("MaxRTT = %v, want 30ms", v.MaxRTT)
	}
	if v.AvgRTT != 20*time.Millisecond {
		t.Errorf("AvgRTT = %v, want 20ms", v.AvgRTT)
	}
	if v.UpCount != 3 {
		t.Errorf("UpCount = %d, want 3", v.UpCount)
	}
}

func TestHTTPCheckResult_RTTStats_DownDoesNotAffectRTT(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	r.SetResult(200, 10*time.Millisecond, nil)
	r.SetResult(500, 99*time.Millisecond, nil) // Down: should not update RTT stats
	v := r.GetView()
	if v.MinRTT != 10*time.Millisecond {
		t.Errorf("MinRTT = %v, want 10ms (Down should not affect RTT)", v.MinRTT)
	}
	if v.MaxRTT != 10*time.Millisecond {
		t.Errorf("MaxRTT = %v, want 10ms (Down should not affect RTT)", v.MaxRTT)
	}
}

func TestHTTPCheckResult_LastChange(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	// Checking... → Up (status change should set LastChange)
	r.SetResult(200, 10*time.Millisecond, nil)
	v1 := r.GetView()
	if v1.LastChange.IsZero() {
		t.Error("LastChange should be set after first status change from Checking... to Up")
	}
	// Same status: no change
	t1 := v1.LastChange
	time.Sleep(time.Millisecond)
	r.SetResult(200, 15*time.Millisecond, nil)
	v2 := r.GetView()
	if !v2.LastChange.Equal(t1) {
		t.Errorf("LastChange updated on same status: got %v, want %v", v2.LastChange, t1)
	}
	// Status changes Up → Down
	time.Sleep(time.Millisecond)
	r.SetResult(503, 0, nil)
	v3 := r.GetView()
	if !v3.LastChange.After(t1) {
		t.Error("LastChange should update on Up→Down transition")
	}
}

func TestHTTPCheckResult_ConcurrentSafe(t *testing.T) {
	const workers, perWorker = 5, 200
	r := NewHTTPCheckResult("https://example.com")
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range perWorker {
				r.SetResult(200, time.Millisecond, nil)
			}
		})
		wg.Go(func() {
			for range perWorker {
				_ = r.GetView()
			}
		})
	}
	wg.Wait()

	if got := r.GetView().UpCount; got != workers*perWorker {
		t.Errorf("UpCount = %d, want %d (concurrent updates were lost)", got, workers*perWorker)
	}
}
