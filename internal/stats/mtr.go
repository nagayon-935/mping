package stats

import (
	"fmt"
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
	Sent    int
	Recv    int
	LastRTT time.Duration
	MinRTT  time.Duration
	MaxRTT  time.Duration
	SumRTT  time.Duration
	jitter  int64 // ns, RFC 1889 smoothing
}

// HopView is the read-only per-hop snapshot for UI/export.
type HopView struct {
	TTL     int
	IP      string // "" when no responder has ever answered (rendered as "*")
	ASN     string
	Country string
	Org     string
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
}

// NewMTRStats returns an empty MTRStats.
func NewMTRStats() *MTRStats {
	return &MTRStats{}
}

// EnsureLen grows the hop slice to n entries if it is currently shorter.
// Existing hop data is preserved; this is a no-op when len >= n.
func (m *MTRStats) EnsureLen(n int) {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hopAt(ttl)
	if h == nil {
		return
	}
	h.Sent++
	h.Recv++
	if ip != "" {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hopAt(ttl)
	if h == nil {
		return
	}
	h.Sent++
}

// HasIP reports whether the hop at ttl has ever had a responder IP recorded
// (i.e. would render as something other than "*"). ttl is 1-based; an
// out-of-range ttl reports false.
func (m *MTRStats) HasIP(ttl int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h := m.hopAt(ttl)
	return h != nil && h.IP != ""
}

// SetIP updates the responder identity for the hop at ttl without a sample.
// Used during discovery when no RTT measurement was taken.
// ttl is 1-based.
func (m *MTRStats) SetIP(ttl int, ip, asn, country, org string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hopAt(ttl)
	if h == nil {
		return
	}
	if ip != "" {
		h.IP = ip
		h.routeIP = ip // track discovery-time IP separately for flap detection
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

	cur := make([]string, len(m.hops))
	for i, h := range m.hops {
		cur[i] = h.routeIP // compare discovery IPs, not live probe IPs
	}

	if routeEqual(prevIPs, cur) {
		return false, ""
	}

	desc := buildFlapDesc(prevIPs, cur)
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

func routeEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	if len(changed) == 1 {
		return changed[0]
	}
	return strings.Join(changed, "; ")
}

// Reset clears all hop data.
func (m *MTRStats) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hops = nil
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
