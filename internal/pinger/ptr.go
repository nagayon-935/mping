package pinger

import (
	"fmt"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// lookupPTR performs a (potentially slow) reverse DNS (PTR) lookup for ipStr
// and records the result on t. Guarded by p.done so that a lookup queued
// just before Stop() doesn't fire at all — mirrors lookupASN exactly,
// including the getPTR -> lookupAddrBounded race against p.done.
func (p *Pinger) lookupPTR(t *stats.TargetStats, ipStr string) {
	select {
	case <-p.done:
		return
	default:
	}
	name := p.getPTR(ipStr)
	if name != "" {
		t.SetPTR(name)
	}
}

// getPTR resolves ipStr's PTR (reverse DNS) record, using ptrCache to avoid
// repeat lookups for the same IP. Returns "" on any failure — no PTR
// record, timeout, or Stop() firing mid-lookup — so callers fall back to
// displaying the plain IP. Failures are deliberately not cached (mirroring
// getASNInfo's NA handling): a transient resolver hiccup shouldn't
// permanently block a later retry.
func (p *Pinger) getPTR(ipStr string) string {
	p.ptrMu.RLock()
	name, found := p.ptrCache[ipStr]
	p.ptrMu.RUnlock()
	if found {
		return name
	}

	if j := p.ptrJitter(); j > 0 {
		select {
		case <-time.After(j):
		case <-p.done:
			return ""
		}
	}

	names, err := p.lookupAddrBounded(ipStr)
	if err != nil || len(names) == 0 {
		return ""
	}

	name = strings.TrimSuffix(names[0], ".")
	if name == "" {
		return ""
	}

	p.ptrMu.Lock()
	p.ptrCache[ipStr] = name
	p.ptrMu.Unlock()
	return name
}

// lookupAddrBounded wraps p.lookupAddr with ptrLookupTimeout and aborts
// early when Stop() closes p.done. Mirrors lookupTXTBounded: net.LookupAddr
// has no cancellation seam, so the lookup goroutine is left to finish into a
// buffered channel while the caller is released immediately, so a hung
// resolver cannot stall Pinger.Wait().
func (p *Pinger) lookupAddrBounded(ipStr string) ([]string, error) {
	type result struct {
		names []string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		names, err := p.lookupAddr(ipStr)
		ch <- result{names, err}
	}()
	select {
	case r := <-ch:
		return r.names, r.err
	case <-p.done:
		return nil, errPingerStopped
	case <-time.After(ptrLookupTimeout):
		return nil, fmt.Errorf("ptr lookup for %q timed out after %s", ipStr, ptrLookupTimeout)
	}
}

// annotateHopIP formats a traceroute/MTR-style hop responder IP, optionally
// appending an ASN annotation (matching TraceRoute's pre-existing
// "IP(ASxxx Org)" format) and/or a PTR reverse-DNS name. ip == "" or "*" is
// returned unchanged since there is no responder to annotate. Extracted out
// of TraceRoute's hop loop so the annotation logic can be unit tested
// without simulating ICMP wire traffic.
func annotateHopIP(ip string, asnEnabled bool, getASN func(string) ASNInfo, ptrEnabled bool, getPTR func(string) string) string {
	if ip == "" || ip == "*" {
		return ip
	}
	out := ip
	if asnEnabled {
		info := getASN(ip)
		if annotation := stats.FormatASN(info.Number, info.Org); annotation != "" {
			out = fmt.Sprintf("%s(%s)", out, annotation)
		}
	}
	if ptrEnabled {
		if name := getPTR(ip); name != "" {
			out = fmt.Sprintf("%s %s", out, name)
		}
	}
	return out
}
