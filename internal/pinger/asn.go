package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// asnLookupTimeout bounds each Cymru DNS TXT query, since neither net.LookupTXT
// nor a *net.Resolver wrapped with context.Background() has a timeout of its
// own. A var (not const) so tests can shrink it instead of waiting 3s.
var asnLookupTimeout = 3 * time.Second

// ASNInfo holds the full result of a Team Cymru ASN lookup.
type ASNInfo struct {
	Number  string // "AS15169"
	Country string // "US"
	Org     string // "Google LLC"
}

// lookupASN runs on a pinger-owned metadata goroutine.
// Its DNS calls share the pinger lifetime, so Stop cancels them and Wait
// joins the metadata goroutine before its statistics can be reused.
func (p *Pinger) lookupASN(t *stats.TargetStats, ipStr string) {
	select {
	case <-p.done:
		return
	default:
	}
	info := p.getASNInfo(ipStr)
	if info.Number != "" {
		t.SetASNInfo(info.Number, info.Country, info.Org)
	}
}

func (p *Pinger) getASNInfo(ipStr string) ASNInfo {
	return p.getASNInfoContext(context.Background(), ipStr)
}

// getASNInfoContext is getASNInfo that gives up when ctx is cancelled,
// without caching anything for ipStr.
func (p *Pinger) getASNInfoContext(ctx context.Context, ipStr string) ASNInfo {
	p.asnMu.RLock()
	info, found := p.asnCache[ipStr]
	p.asnMu.RUnlock()
	if found {
		return info
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ASNInfo{}
	}

	if j := p.asnJitter(); j > 0 {
		select {
		case <-time.After(j):
		case <-p.done:
			return ASNInfo{}
		case <-ctx.Done():
			return ASNInfo{}
		}
	}

	var originQuery string
	if ip4 := ip.To4(); ip4 != nil {
		originQuery = fmt.Sprintf("%d.%d.%d.%d.origin.asn.cymru.com", ip4[3], ip4[2], ip4[1], ip4[0])
	} else {
		var sb strings.Builder
		for i := 15; i >= 0; i-- {
			sb.WriteString(fmt.Sprintf("%x.%x.", ip[i]&0xf, ip[i]>>4))
		}
		sb.WriteString("origin6.asn.cymru.com")
		originQuery = sb.String()
	}

	txts, err := p.lookupTXTBoundedContext(ctx, originQuery)
	if err != nil || len(txts) == 0 {
		return ASNInfo{}
	}

	// Response example: "15169 | 8.8.8.0/24 | US | arin | 1992-12-01"
	parts := strings.Split(txts[0], "|")

	asnRaw := strings.TrimSpace(parts[0])
	if asnRaw == "NA" || asnRaw == "" {
		return ASNInfo{}
	}
	if !strings.HasPrefix(asnRaw, "AS") {
		asnRaw = "AS" + asnRaw
	}

	var country string
	if len(parts) >= 3 {
		country = strings.TrimSpace(parts[2])
	}

	// Second lookup: <asn-number>.asn.cymru.com for org name
	// Response: "15169 | GOOGLE - Google LLC, US | 1992-12-01"
	org, err := p.lookupOrgContext(ctx, strings.TrimPrefix(asnRaw, "AS"))
	if errors.Is(err, errPingerStopped) || ctx.Err() != nil {
		// Stop() fired mid-lookup: don't cache a partial record (real ASN/
		// country with a bogus empty Org that's indistinguishable from a
		// genuine "no org found" result).
		return ASNInfo{}
	}

	info = ASNInfo{Number: asnRaw, Country: country, Org: org}
	p.asnMu.Lock()
	p.asnCache[ipStr] = info
	p.asnMu.Unlock()
	return info
}

// lookupTXTBoundedContext runs cancellable DNS on the calling goroutine.
// Only explicitly injected context-free hooks use the legacy bounded adapter.
func (p *Pinger) lookupTXTBoundedContext(ctx context.Context, name string) ([]string, error) {
	return lookupBoundedContext(ctx, p, asnLookupTimeout, "asn", name, p.lookupTXTContext, p.lookupTXT)
}

// lookupOrgContext resolves the org name for asnNumber. The returned error is
// propagated when Stop() closes p.done (errPingerStopped) or ctx is
// cancelled; a genuine DNS failure or empty/malformed response is
// reported as ("", nil) so callers keep treating it as "no org found", not
// as a reason to discard an otherwise-successful ASN/country result.
func (p *Pinger) lookupOrgContext(ctx context.Context, asnNumber string) (string, error) {
	txts, err := p.lookupTXTBoundedContext(ctx, asnNumber+".asn.cymru.com")
	if errors.Is(err, errPingerStopped) || ctx.Err() != nil {
		return "", err
	}
	if err != nil || len(txts) == 0 {
		return "", nil
	}
	// Response: "15169 | GOOGLE - Google LLC, US | 1992-12-01"
	parts := strings.Split(txts[0], "|")
	if len(parts) < 2 {
		return "", nil
	}
	desc := strings.TrimSpace(parts[1]) // "GOOGLE - Google LLC, US"
	// Strip "HANDLE - " prefix if present
	if idx := strings.Index(desc, " - "); idx >= 0 {
		desc = strings.TrimSpace(desc[idx+3:]) // "Google LLC, US"
	}
	// Strip trailing ", CC" country suffix
	if idx := strings.LastIndex(desc, ", "); idx >= 0 {
		desc = strings.TrimSpace(desc[:idx]) // "Google LLC"
	}
	return desc, nil
}
