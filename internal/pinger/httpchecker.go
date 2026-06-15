package pinger

import (
	"net/http"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// HTTPChecker runs HTTP(S) GET health checks for a set of URLs.
type HTTPChecker struct {
	results  []*stats.HTTPCheckResult
	interval time.Duration
	client   *http.Client
	done     chan struct{}
	stopOnce sync.Once
}

// NewHTTPChecker creates an HTTPChecker for the given URLs.
func NewHTTPChecker(urls []string, interval, timeout time.Duration) *HTTPChecker {
	results := make([]*stats.HTTPCheckResult, len(urls))
	for i, u := range urls {
		results[i] = stats.NewHTTPCheckResult(u)
	}
	return &HTTPChecker{
		results:  results,
		interval: interval,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		done: make(chan struct{}),
	}
}

// Results returns the per-URL check result slice (callers must not modify it).
func (hc *HTTPChecker) Results() []*stats.HTTPCheckResult {
	return hc.results
}

// Start launches one goroutine per URL. Safe to call once.
func (hc *HTTPChecker) Start() {
	for _, r := range hc.results {
		go hc.loop(r)
	}
}

// Stop signals all goroutines to exit. Safe to call multiple times.
func (hc *HTTPChecker) Stop() {
	hc.stopOnce.Do(func() { close(hc.done) })
}

func (hc *HTTPChecker) loop(r *stats.HTTPCheckResult) {
	hc.check(r)
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-hc.done:
			return
		case <-ticker.C:
			hc.check(r)
		}
	}
}

func (hc *HTTPChecker) check(r *stats.HTTPCheckResult) {
	start := time.Now()
	resp, err := hc.client.Get(r.URL) //nolint:noctx
	rtt := time.Since(start)
	if err != nil {
		r.SetResult(0, rtt, err)
		return
	}
	resp.Body.Close()
	r.SetResult(resp.StatusCode, rtt, nil)
}
