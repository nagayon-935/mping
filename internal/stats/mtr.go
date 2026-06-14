package stats

import (
	"sync"
	"time"
)

// HopStats accumulates per-hop probe statistics for one TTL position.
type HopStats struct {
	TTL     int    // hop number (1-based)
	IP      string // last-seen responder IP ("" if never responded)
	ASN     string // cached ASN annotation
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
	mu   sync.RWMutex
	hops []*HopStats
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

// RecordReply records a successful probe response for the hop at ttl.
// ttl is 1-based.
func (m *MTRStats) RecordReply(ttl int, ip, asn string, rtt time.Duration) {
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
	// RFC 1889 jitter
	if h.Recv > 1 {
		delta := int64(rtt - h.LastRTT)
		if delta < 0 {
			delta = -delta
		}
		h.jitter += (delta - h.jitter) / jitterDivisor
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

// SetIP updates the responder identity for the hop at ttl without a sample.
// Used during discovery when no RTT measurement was taken.
// ttl is 1-based.
func (m *MTRStats) SetIP(ttl int, ip, asn string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hopAt(ttl)
	if h == nil {
		return
	}
	if ip != "" {
		h.IP = ip
	}
	if asn != "" {
		h.ASN = asn
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
