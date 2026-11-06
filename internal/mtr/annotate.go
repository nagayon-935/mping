package mtr

import (
	"sync"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// maxConcurrentHopLookups bounds one target's in-flight ASN/PTR lookups.
const maxConcurrentHopLookups = 4

// hopAnnotator resolves hop ASN and PTR annotations off the probing path.
//
// Discovery and probe rounds register a hop's IP immediately and hand the IP
// to note; the lookups (Team Cymru TXT and reverse DNS, which can each take
// seconds) run in the background, once per distinct IP, and are applied only
// to hops that still answer from that IP. Before this, discovery waited for
// every hop's lookups in turn, so with -a/-r hops appeared seconds apart.
//
// A nil *hopAnnotator is valid and annotates nothing (tests that drive
// discover/probe directly).
type hopAnnotator struct {
	prober HopProber
	mtr    *stats.MTRStats
	sem    chan struct{}

	mu       sync.Mutex
	closed   bool
	known    map[string]hopInfo
	inflight map[string]bool
}

type hopInfo struct {
	asn pinger.ASNInfo
	ptr string
}

func newHopAnnotator(prober HopProber, mtr *stats.MTRStats) *hopAnnotator {
	return &hopAnnotator{
		prober:   prober,
		mtr:      mtr,
		sem:      make(chan struct{}, maxConcurrentHopLookups),
		known:    make(map[string]hopInfo),
		inflight: make(map[string]bool),
	}
}

// note records that ip answers at some hop. A finished lookup is re-applied
// at once (the hop may have just switched to ip, which clears annotations);
// otherwise a background lookup is started unless one is already running.
func (a *hopAnnotator) note(ip string) {
	if a == nil || ip == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	if info, ok := a.known[ip]; ok {
		a.mtr.AnnotateIP(ip, info.asn.Number, info.asn.Country, info.asn.Org, info.ptr)
		return
	}
	if a.inflight[ip] {
		return
	}
	a.inflight[ip] = true
	go a.lookup(ip)
}

func (a *hopAnnotator) lookup(ip string) {
	a.sem <- struct{}{}
	defer func() { <-a.sem }()
	if a.isClosed() {
		return
	}
	asn := a.prober.ASNInfoFor(ip)
	ptr := a.prober.PTRFor(ip)

	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inflight, ip)
	if a.closed {
		return
	}
	a.known[ip] = hopInfo{asn: asn, ptr: ptr}
	a.mtr.AnnotateIP(ip, asn.Number, asn.Country, asn.Org, ptr)
}

func (a *hopAnnotator) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// close stops all annotation. It does not wait for lookups that are still
// blocked in DNS (they have no context); their results are discarded, and
// none is written after close returns.
func (a *hopAnnotator) close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
}
