package stats

import (
	"sync"
	"time"
)

// HTTPCheckResult accumulates per-URL HTTP health check statistics.
type HTTPCheckResult struct {
	URL        string
	StatusCode int
	Status     string // "Up", "Down", "Error", "Checking..."
	RTT        time.Duration
	UpCount    int
	DownCount  int
	LastChange time.Time

	rtt rttAccumulator

	mu sync.RWMutex
}

// HTTPCheckView is a read-only snapshot of HTTPCheckResult.
type HTTPCheckView struct {
	URL        string
	StatusCode int
	Status     string
	RTT        time.Duration
	MinRTT     time.Duration
	AvgRTT     time.Duration
	MaxRTT     time.Duration
	UpCount    int
	DownCount  int
	LastChange time.Time
}

// NewHTTPCheckResult creates a new HTTPCheckResult for the given URL.
func NewHTTPCheckResult(url string) *HTTPCheckResult {
	return &HTTPCheckResult{URL: url, Status: "Checking..."}
}

// SetResult records the outcome of one HTTP health check.
// statusCode is 0 when err is non-nil.
func (r *HTTPCheckResult) SetResult(statusCode int, rtt time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var status string
	switch {
	case err != nil:
		status = "Error"
	case statusCode >= 200 && statusCode < 400:
		status = "Up"
	default:
		status = "Down"
	}

	if status != r.Status {
		r.LastChange = time.Now()
	}
	r.Status = status
	r.StatusCode = statusCode
	r.RTT = rtt

	switch status {
	case "Up":
		r.UpCount++
		r.recordRTT(rtt)
	default:
		r.DownCount++
	}
}

func (r *HTTPCheckResult) recordRTT(rtt time.Duration) {
	r.rtt.record(rtt)
}

// GetView returns a thread-safe snapshot.
func (r *HTTPCheckResult) GetView() HTTPCheckView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return HTTPCheckView{
		URL:        r.URL,
		StatusCode: r.StatusCode,
		Status:     r.Status,
		RTT:        r.RTT,
		MinRTT:     r.rtt.min,
		AvgRTT:     r.rtt.avg(),
		MaxRTT:     r.rtt.max,
		UpCount:    r.UpCount,
		DownCount:  r.DownCount,
		LastChange: r.LastChange,
	}
}
