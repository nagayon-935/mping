package pinger

import (
	"context"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// ptrLookupTimeout bounds each PTR (reverse DNS) query, since neither
// net.LookupAddr nor a *net.Resolver wrapped with context.Background() has a
// timeout of its own. A var (not const) so tests can shrink it instead of
// waiting 3s. Mirrors asnLookupTimeout.
var ptrLookupTimeout = 3 * time.Second

// lookupPTR performs a (potentially slow) reverse DNS (PTR) lookup for ipStr
// and records the result on t. The pinger lifetime cancels its DNS calls;
// Wait joins the metadata goroutine before the target statistics are reused.
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
	return p.getPTRContext(context.Background(), ipStr)
}

// getPTRContext is getPTR that gives up when ctx is cancelled.
func (p *Pinger) getPTRContext(ctx context.Context, ipStr string) string {
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
		case <-ctx.Done():
			return ""
		}
	}

	names, err := lookupBoundedContext(ctx, p, ptrLookupTimeout, "ptr", ipStr, p.lookupAddrContext, p.lookupAddr)
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
