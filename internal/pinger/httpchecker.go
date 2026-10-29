package pinger

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

const maxRedirects = 10

// maxDrainBytes caps how much of a response body the checker reads. The body
// is drained only so the connection can go back into the keep-alive pool —
// a health check has no use for the content — and client.Timeout bounds how
// long an oversized body takes to read but not how much of it crosses the
// wire.
const maxDrainBytes = 64 << 10

// drainBody reads and discards up to maxDrainBytes so the connection can be
// reused. The limit is one byte past the cap on purpose: io.CopyN stops at
// its limit without ever reading beyond it, so asking for exactly
// maxDrainBytes would leave a maxDrainBytes-sized body one read short of
// EOF — net/http would treat it as unfinished and drop the connection, the
// very cost this helper exists to avoid. A body past the cap is left unread,
// which costs that one connection instead of the rest of the transfer.
func drainBody(body io.Reader) {
	_, _ = io.CopyN(io.Discard, body, maxDrainBytes+1)
}

// HTTPChecker runs HTTP(S) GET health checks for a set of URLs.
type HTTPChecker struct {
	results  []*stats.HTTPCheckResult
	interval time.Duration
	bind     BindConfig
	client   *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	options  HTTPOptions
}

// NewHTTPChecker creates an HTTPChecker for the given URLs. bind carries the
// -S source address and -I interface name so HTTP checks leave the host by the
// same path as the ICMP probes; a zero BindConfig leaves http.DefaultTransport
// in place, exactly as before those flags were wired in.
func NewHTTPChecker(urls []string, interval, timeout time.Duration, bind BindConfig) *HTTPChecker {
	return NewHTTPCheckerWithOptions(urls, interval, timeout, bind, DefaultHTTPOptions())
}

func NewHTTPCheckerWithOptions(urls []string, interval, timeout time.Duration, bind BindConfig, options HTTPOptions) *HTTPChecker {
	results := make([]*stats.HTTPCheckResult, len(urls))
	for i, u := range urls {
		results[i] = stats.NewHTTPCheckResult(u)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hc := &HTTPChecker{
		results:  results,
		interval: interval,
		bind:     bind,
		ctx:      ctx,
		cancel:   cancel,
		options:  options.Clone(),
	}
	hc.client = &http.Client{Timeout: timeout, CheckRedirect: hc.checkRedirect}
	// Assign only when a bound transport is actually needed: leaving Transport
	// nil keeps http.DefaultTransport, whereas assigning a nil *http.Transport
	// would store a non-nil interface holding a nil pointer.
	if tr := newBoundTransport(timeout, bind); tr != nil {
		hc.client.Transport = tr
	}
	return hc
}

// checkRedirect is the client's redirect policy. It reads hc.options, the
// checker's own copy, so it always agrees with the headers check sends.
func (hc *HTTPChecker) checkRedirect(req *http.Request, via []*http.Request) error {
	if !hc.options.FollowRedirects || len(via) >= maxRedirects {
		return http.ErrUseLastResponse
	}
	// Custom API-key headers are not covered by net/http's built-in
	// Authorization/Cookie filtering. Keep configured headers on the
	// original origin only, including across HTTPS -> HTTP redirects.
	if len(via) > 0 && (req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host) {
		for name := range hc.options.Headers {
			req.Header.Del(name)
		}
	}
	return nil
}

// BindConfig returns the source/interface binding the checks are dialling
// with.
func (hc *HTTPChecker) BindConfig() BindConfig { return hc.bind }

// Results returns the per-URL check result slice (callers must not modify it).
func (hc *HTTPChecker) Results() []*stats.HTTPCheckResult {
	return hc.results
}

// Start launches one goroutine per URL. Safe to call once.
func (hc *HTTPChecker) Start() {
	for _, r := range hc.results {
		hc.wg.Add(1)
		go hc.loop(r)
	}
}

// Stop signals all goroutines to exit. Safe to call multiple times.
func (hc *HTTPChecker) Stop() {
	hc.cancel()
}

// Wait blocks until all check goroutines have exited. Call Stop first.
func (hc *HTTPChecker) Wait() {
	hc.wg.Wait()
}

func (hc *HTTPChecker) loop(r *stats.HTTPCheckResult) {
	defer hc.wg.Done()
	runPeriodic(hc.ctx, hc.interval, func() { hc.check(r) })
}

func (hc *HTTPChecker) check(r *stats.HTTPCheckResult) {
	if hc.ctx.Err() != nil {
		return
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(hc.ctx, "GET", r.URL, nil)
	if err != nil {
		r.SetResult(0, 0, err)
		return
	}
	req.Header = hc.options.Headers.Clone()
	resp, err := hc.client.Do(req)
	rtt := time.Since(start)
	if err != nil {
		if hc.ctx.Err() != nil {
			return // preserve the last measurement when shutting down
		}
		r.SetResult(0, rtt, err)
		return
	}
	defer resp.Body.Close()
	drainBody(resp.Body)
	r.SetExpectedResult(resp.StatusCode, rtt, nil, hc.options.Accepts(resp.StatusCode))
}
