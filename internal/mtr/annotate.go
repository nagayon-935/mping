package mtr

import (
	"context"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// maxConcurrentHopLookups bounds one target's in-flight ASN/PTR lookups.
const maxConcurrentHopLookups = 4

// defaultAnnotationRetry is how long an incomplete lookup result (no ASN or
// no PTR, including a transient DNS failure) is kept before it is retried.
const defaultAnnotationRetry = 30 * time.Second

// hopAnnotator resolves hop ASN and PTR annotations off the probing path.
//
// Discovery and probe rounds register a hop's IP immediately, together with
// any annotation already known for that IP, and hand the IP to note; lookups
// (Team Cymru TXT and reverse DNS, which can each take seconds) run in the
// background, once per IP, and are applied only to hops that still answer
// from that IP.
//
// Incomplete results are retried after retry, mirroring pinger, which
// deliberately does not cache failures. close cancels running lookups and
// waits for them, so none outlives the target.
//
// A nil *hopAnnotator is valid and annotates nothing (tests that drive
// discover/probe directly).
type hopAnnotator struct {
	ctx     context.Context
	cancel  context.CancelFunc
	prober  HopProber
	mtr     *stats.MTRStats
	sem     chan struct{}
	retry   time.Duration
	wantASN bool
	wantPTR bool
	now     func() time.Time
	wg      sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	known    map[string]hopInfo
	inflight map[string]bool
}

type hopInfo struct {
	ann      stats.HopAnnotation
	complete bool
	retryAt  time.Time
}

func newHopAnnotator(parent context.Context, prober HopProber, mtr *stats.MTRStats, retry time.Duration, wantASN, wantPTR bool) *hopAnnotator {
	if !wantASN && !wantPTR {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	if retry <= 0 {
		retry = defaultAnnotationRetry
	}
	return &hopAnnotator{
		ctx:      ctx,
		cancel:   cancel,
		prober:   prober,
		mtr:      mtr,
		sem:      make(chan struct{}, maxConcurrentHopLookups),
		retry:    retry,
		wantASN:  wantASN,
		wantPTR:  wantPTR,
		now:      time.Now,
		known:    make(map[string]hopInfo),
		inflight: make(map[string]bool),
	}
}

// cached returns what is known for ip, so a hop can be recorded with its
// annotation in the same update (no blank flicker when an ECMP hop
// alternates between routers).
func (a *hopAnnotator) cached(ip string) stats.HopAnnotation {
	if a == nil {
		return stats.HopAnnotation{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.known[ip].ann
}

// note starts a background lookup for ip unless one is running, the result
// is complete, or an incomplete result is not yet due for a retry.
func (a *hopAnnotator) note(ip string) {
	if a == nil || ip == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.inflight[ip] {
		return
	}
	if info, ok := a.known[ip]; ok && (info.complete || a.now().Before(info.retryAt)) {
		return
	}
	a.inflight[ip] = true
	a.wg.Go(func() { a.lookup(ip) })
}

func (a *hopAnnotator) lookup(ip string) {
	defer func() {
		a.mu.Lock()
		delete(a.inflight, ip)
		a.mu.Unlock()
	}()
	select {
	case a.sem <- struct{}{}:
	case <-a.ctx.Done():
		return
	}
	defer func() { <-a.sem }()
	if a.ctx.Err() != nil {
		return
	}
	var asn pinger.ASNInfo
	if a.wantASN {
		asn = a.prober.ASNInfoFor(a.ctx, ip)
	}
	var ptr string
	if a.wantPTR {
		ptr = a.prober.PTRFor(a.ctx, ip)
	}

	a.mu.Lock()
	if a.closed || a.ctx.Err() != nil {
		a.mu.Unlock()
		return
	}
	merged := a.rememberLocked(ip, asn, ptr)
	a.mu.Unlock()
	if merged != (stats.HopAnnotation{}) {
		a.mtr.AnnotateIP(ip, merged.ASN, merged.Country, merged.Org, merged.PTR)
	}
}

// rememberLocked merges lookup results, retaining earlier values a retry lacks.
// The caller must hold a.mu.
func (a *hopAnnotator) rememberLocked(ip string, asn pinger.ASNInfo, ptr string) stats.HopAnnotation {
	merged := a.known[ip].ann
	if asn.Number != "" {
		merged.ASN, merged.Country, merged.Org = asn.Number, asn.Country, asn.Org
	}
	if ptr != "" {
		merged.PTR = ptr
	}
	a.known[ip] = hopInfo{
		ann:      merged,
		complete: (!a.wantASN || merged.ASN != "") && (!a.wantPTR || merged.PTR != ""),
		retryAt:  a.now().Add(a.retry),
	}
	return merged
}

// close cancels running lookups and waits for them; nothing is annotated
// and the prober is not called after close returns.
func (a *hopAnnotator) close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.cancel()
	a.wg.Wait()
}
