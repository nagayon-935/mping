package stats

import (
	"errors"
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

func TestHTTPCheckResult_SetResult_Up2xx(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	r.SetResult(200, 10*time.Millisecond, nil)
	v := r.GetView()
	if v.Status != "Up" {
		t.Errorf("Status = %q, want Up", v.Status)
	}
	if v.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", v.StatusCode)
	}
	if v.UpCount != 1 {
		t.Errorf("UpCount = %d, want 1", v.UpCount)
	}
	if v.DownCount != 0 {
		t.Errorf("DownCount = %d, want 0", v.DownCount)
	}
	if v.MinRTT != 10*time.Millisecond {
		t.Errorf("MinRTT = %v, want 10ms", v.MinRTT)
	}
}

func TestHTTPCheckResult_SetResult_Up3xx(t *testing.T) {
	r := NewHTTPCheckResult("http://example.com")
	r.SetResult(301, 5*time.Millisecond, nil)
	v := r.GetView()
	if v.Status != "Up" {
		t.Errorf("Status = %q, want Up (3xx counts as Up)", v.Status)
	}
	if v.UpCount != 1 {
		t.Errorf("UpCount = %d, want 1", v.UpCount)
	}
}

func TestHTTPCheckResult_SetResult_Down4xx(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	r.SetResult(404, 20*time.Millisecond, nil)
	v := r.GetView()
	if v.Status != "Down" {
		t.Errorf("Status = %q, want Down", v.Status)
	}
	if v.DownCount != 1 {
		t.Errorf("DownCount = %d, want 1", v.DownCount)
	}
	if v.UpCount != 0 {
		t.Errorf("UpCount = %d, want 0", v.UpCount)
	}
}

func TestHTTPCheckResult_SetResult_Down5xx(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	r.SetResult(503, 15*time.Millisecond, nil)
	v := r.GetView()
	if v.Status != "Down" {
		t.Errorf("Status = %q, want Down", v.Status)
	}
}

func TestHTTPCheckResult_SetResult_Error(t *testing.T) {
	r := NewHTTPCheckResult("https://example.com")
	r.SetResult(0, 0, errors.New("connection refused"))
	v := r.GetView()
	if v.Status != "Error" {
		t.Errorf("Status = %q, want Error", v.Status)
	}
	if v.DownCount != 1 {
		t.Errorf("DownCount = %d, want 1", v.DownCount)
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
	r := NewHTTPCheckResult("https://example.com")
	done := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					r.SetResult(200, time.Millisecond, nil)
				}
			}
		}()
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					r.GetView()
				}
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(done)
}
