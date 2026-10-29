package stats

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// HopStats accumulates per-hop probe statistics for one TTL position.
type HopStats struct {
	TTL     int    // hop number (1-based)
	IP      string // live display IP (updated by both discovery and probe replies)
	routeIP string // IP from last explicit discovery (SetIP only); used for flap detection
	ASN     string // cached ASN annotation
	Country string // two-letter country code (from Cymru)
	Org     string // organization name (from Cymru)
	PTR     string // reverse DNS name of IP, "" when unknown
	Sent    int
	Recv    int
	LastRTT time.Duration
	MinRTT  time.Duration
	MaxRTT  time.Duration
	SumRTT  time.Duration
	jitter  int64 // ns, RFC 1889 smoothing
}

// annotate overwrites IP/ASN/Country/Org with each non-empty argument; empty
// arguments leave the existing value untouched. A new responder IP drops the
// previous one's PTR name. Caller must hold the owning MTRStats.mu.
func (h *HopStats) annotate(ip, asn, country, org string) {
	if ip != "" {
		if ip != h.IP {
			h.PTR = ""
		}
		h.IP = ip
	}
	if asn != "" {
		h.ASN = asn
	}
	if country != "" {
		h.Country = country
	}
	if org != "" {
		h.Org = org
	}
}

// HopView is the read-only per-hop snapshot for UI/export.
type HopView struct {
	TTL     int
	IP      string // "" when no responder has ever answered (rendered as "*")
	ASN     string
	Country string
	Org     string
	PTR     string
	Sent    int
	Recv    int
	LossPct float64
	LastRTT time.Duration
	AvgRTT  time.Duration
	MinRTT  time.Duration
	MaxRTT  time.Duration
	Jitter  time.Duration
}

// MTRStats holds the full hop path and per-hop stats for one target.
// Self-locking so the MTR engine never contends on TargetStats.mu.
type MTRStats struct {
	mu           sync.RWMutex
	hops         []*HopStats
	flapCount    int
	lastFlapAt   time.Time
	lastFlapDesc string
	lastErr      string // why the route could not be discovered, "" once it was
}

// NewMTRStats returns an empty MTRStats.
func NewMTRStats() *MTRStats {
	return &MTRStats{}
}

// EnsureLen grows the hop slice to n entries if it is currently shorter.
// Existing hop data is preserved; this is a no-op when len >= n.
func (m *MTRStats) EnsureLen(n int) {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	for len(m.hops) < n {
		ttl := len(m.hops) + 1
		m.hops = append(m.hops, &HopStats{TTL: ttl})
	}
}

// TruncateLen shrinks the hop slice to n entries if it is currently longer,
// discarding stale trailing hops. Used after a rediscovery finds a shorter
// path so the UI stops rendering frozen data for hops that no longer exist.
// This is a no-op when len <= n.
func (m *MTRStats) TruncateLen(n int) {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 0 {
		n = 0
	}
	if len(m.hops) > n {
		m.hops = m.hops[:n]
	}
}

// RecordReply records a successful probe response for the hop at ttl.
// ttl is 1-based.
func (m *MTRStats) RecordReply(ttl int, ip, asn, country, org string, rtt time.Duration) {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hopAt(ttl)
	if h == nil {
		return
	}
	h.Sent++
	h.Recv++
	h.annotate(ip, asn, country, org)
	// RFC 1889 jitter
	if h.Recv > 1 {
		h.jitter = updateJitter(h.jitter, rtt, h.LastRTT)
	}
	h.LastRTT = rtt
	h.SumRTT += rtt
	if h.MinRTT == 0 || rtt < h.MinRTT {
		h.MinRTT = rtt
	}
	if rtt > h.MaxRTT {
		h.MaxRTT = rtt
	}
}

// RecordLoss records a probe timeout (no response) for the hop at ttl.
// ttl is 1-based.
func (m *MTRStats) RecordLoss(ttl int) {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hopAt(ttl)
	if h == nil {
		return
	}
	h.Sent++
}

// SetIP updates the responder identity for the hop at ttl without a sample.
// Used during discovery when no RTT measurement was taken.
// ttl is 1-based.
func (m *MTRStats) SetIP(ttl int, ip, asn, country, org string) {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hopAt(ttl)
	if h == nil {
		return
	}
	h.annotate(ip, asn, country, org)
	if ip != "" {
		h.routeIP = ip // track discovery-time IP separately for flap detection
	}
}

// SetPTR records the reverse DNS name of the hop at ttl, provided ip is
// still that hop's responder. ttl is 1-based.
func (m *MTRStats) SetPTR(ttl int, ip, name string) {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	if h := m.hopAt(ttl); h != nil && h.IP == ip {
		h.PTR = name
	}
}

// SetError records why the route could not be discovered; "" clears it.
func (m *MTRStats) SetError(msg string) {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = msg
}

// Error returns the last route discovery error, or "".
func (m *MTRStats) Error() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastErr
}

// View returns an ordered read-only snapshot of all hops.
func (m *MTRStats) View() []HopView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	views := make([]HopView, len(m.hops))
	for i, h := range m.hops {
		var avg time.Duration
		if h.Recv > 0 {
			avg = h.SumRTT / time.Duration(h.Recv)
		}
		var lossPct float64
		if h.Sent > 0 {
			lossPct = float64(h.Sent-h.Recv) / float64(h.Sent) * 100
		}
		views[i] = HopView{
			TTL:     h.TTL,
			IP:      h.IP,
			ASN:     h.ASN,
			Country: h.Country,
			Org:     h.Org,
			PTR:     h.PTR,
			Sent:    h.Sent,
			Recv:    h.Recv,
			LossPct: lossPct,
			LastRTT: h.LastRTT,
			AvgRTT:  avg,
			MinRTT:  h.MinRTT,
			MaxRTT:  h.MaxRTT,
			Jitter:  time.Duration(h.jitter),
		}
	}
	return views
}

// RouteSnapshot returns the ordered discovery-time hop IPs for later comparison.
// Uses routeIP (set only by SetIP/discovery) rather than the live IP field
// (which can be updated by probe replies), so probe-reply IP updates do not
// mask route changes between successive discoveries.
func (m *MTRStats) RouteSnapshot() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.routeIPsLocked()
}

// routeIPsLocked returns the ordered discovery-time hop IPs (routeIP, not the
// live IP field). Caller must hold m.mu (read or write).
func (m *MTRStats) routeIPsLocked() []string {
	ips := make([]string, len(m.hops))
	for i, h := range m.hops {
		ips[i] = h.routeIP
	}
	return ips
}

// CheckFlap compares the current route against prevIPs (from a prior RouteSnapshot).
// If they differ, a flap is recorded and (true, description) is returned.
func (m *MTRStats) CheckFlap(prevIPs []string, now time.Time) (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur := m.routeIPsLocked() // compare discovery IPs, not live probe IPs

	if slices.Equal(prevIPs, cur) {
		return false, ""
	}

	desc := buildFlapDesc(prevIPs, cur)
	bumpGeneration()
	m.flapCount++
	m.lastFlapAt = now
	m.lastFlapDesc = desc
	return true, desc
}

// FlapInfo returns the cumulative flap count, timestamp and description of the last flap.
func (m *MTRStats) FlapInfo() (count int, at time.Time, desc string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.flapCount, m.lastFlapAt, m.lastFlapDesc
}

func buildFlapDesc(prev, cur []string) string {
	if len(prev) != len(cur) {
		return fmt.Sprintf("path length: %d → %d hops", len(prev), len(cur))
	}
	var changed []string
	for i := range prev {
		if prev[i] != cur[i] {
			old := prev[i]
			if old == "" {
				old = "*"
			}
			nw := cur[i]
			if nw == "" {
				nw = "*"
			}
			changed = append(changed, fmt.Sprintf("hop %d: %s → %s", i+1, old, nw))
		}
	}
	return strings.Join(changed, "; ")
}

// Reset clears all hop data, the flap history and the last error. The flap
// fields go with the hops: they describe route changes observed over a
// monitoring window that a reset discards, so leaving them behind would
// report a flap against a window the user just threw away.
func (m *MTRStats) Reset() {
	defer bumpGeneration()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hops = nil
	m.flapCount = 0
	m.lastFlapAt = time.Time{}
	m.lastFlapDesc = ""
	m.lastErr = ""
}

// hopAt returns the HopStats for ttl (1-based), or nil if out of range.
// Caller must hold m.mu.
func (m *MTRStats) hopAt(ttl int) *HopStats {
	idx := ttl - 1
	if idx < 0 || idx >= len(m.hops) {
		return nil
	}
	return m.hops[idx]
}
