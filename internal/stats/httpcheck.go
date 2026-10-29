package stats

import (
	"sync"
	"time"
)

// HTTPCheckResult accumulates per-URL HTTP health check statistics. Its
// fields are guarded by mu; read them through GetView.
type HTTPCheckResult struct {
	url        string
	statusCode int
	status     string // "Up", "Down", "Error", "Checking..."
	lastRTT    time.Duration
	upCount    int
	downCount  int
	lastChange time.Time

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
	StdDev     time.Duration // Population stddev, via Welford's online algorithm
	UpCount    int
	DownCount  int
	LastChange time.Time
}

// NewHTTPCheckResult creates a new HTTPCheckResult for the given URL.
func NewHTTPCheckResult(url string) *HTTPCheckResult {
	return &HTTPCheckResult{url: url, status: "Checking..."}
}

// URL returns the checked URL, which never changes after construction.
func (r *HTTPCheckResult) URL() string { return r.url }

// SetResult records the outcome of one HTTP health check.
// statusCode is 0 when err is non-nil.
func (r *HTTPCheckResult) SetResult(statusCode int, rtt time.Duration, err error) {
	r.SetExpectedResult(statusCode, rtt, err, statusCode >= 200 && statusCode < 400)
}

func (r *HTTPCheckResult) SetExpectedResult(statusCode int, rtt time.Duration, err error, expected bool) {
	defer bumpGeneration()
	r.mu.Lock()
	defer r.mu.Unlock()

	var status string
	switch {
	case err != nil:
		status = "Error"
	case expected:
		status = "Up"
	default:
		status = "Down"
	}

	if status != r.status {
		r.lastChange = time.Now()
	}
	r.status = status
	r.statusCode = statusCode
	r.lastRTT = rtt

	switch status {
	case "Up":
		r.upCount++
		r.rtt.record(rtt)
	default:
		r.downCount++
	}
}

// GetView returns a thread-safe snapshot.
func (r *HTTPCheckResult) GetView() HTTPCheckView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return HTTPCheckView{
		URL:        r.url,
		StatusCode: r.statusCode,
		Status:     r.status,
		RTT:        r.lastRTT,
		MinRTT:     r.rtt.min,
		AvgRTT:     r.rtt.avg(),
		MaxRTT:     r.rtt.max,
		StdDev:     r.rtt.stddev(),
		UpCount:    r.upCount,
		DownCount:  r.downCount,
		LastChange: r.lastChange,
	}
}
