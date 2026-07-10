package stats

import (
	"sync"
	"time"
)

const (
	historySize   = 3000 // 30s of data at the minimum UI refresh rate (100ms per point)
	jitterDivisor = 16   // RFC 1889 §A.8 smoothing factor: J = J + (|D| - J) / 16
)

// PortCheckResult holds the result of a single TCP/UDP port check.
type PortCheckResult struct {
	Port        int
	Protocol    string // "tcp" or "udp"
	Status      string // "Open", "Closed", "Filtered", "Open|Filtered", "Error"
	RTT         time.Duration
	OpenCount   int
	ClosedCount int
	LastChange  time.Time

	// RTT statistics for Open responses only.
	minRTT     time.Duration
	maxRTT     time.Duration
	sumRTT     time.Duration
	rttSamples int

	// Ring buffer for RTT graph (same layout as TargetStats.rttHistory).
	rttHistory []time.Duration
	historyIdx int
	historyLen int

	mu sync.RWMutex
}

func (r *PortCheckResult) SetResult(status string, rtt time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if status != r.Status {
		r.LastChange = time.Now()
	}
	r.Status = status
	r.RTT = rtt
	switch status {
	case "Open":
		r.OpenCount++
		r.recordRTT(rtt)
	case "Closed", "Filtered", "Open|Filtered":
		r.ClosedCount++
	}
}

// recordRTT updates RTT statistics and the ring buffer. Must be called with mu held.
func (r *PortCheckResult) recordRTT(rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	r.rttSamples++
	r.sumRTT += rtt
	if r.minRTT == 0 || rtt < r.minRTT {
		r.minRTT = rtt
	}
	if rtt > r.maxRTT {
		r.maxRTT = rtt
	}
	if r.rttHistory == nil {
		r.rttHistory = make([]time.Duration, historySize)
	}
	r.rttHistory[r.historyIdx%historySize] = rtt
	r.historyIdx++
	if r.historyLen < historySize {
		r.historyLen++
	}
}

// GetView returns a thread-safe snapshot including RTT statistics and history.
func (r *PortCheckResult) GetView() PortCheckView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var avg time.Duration
	if r.rttSamples > 0 {
		avg = r.sumRTT / time.Duration(r.rttSamples)
	}
	return PortCheckView{
		Port:        r.Port,
		Protocol:    r.Protocol,
		Status:      r.Status,
		RTT:         r.RTT,
		MinRTT:      r.minRTT,
		AvgRTT:      avg,
		MaxRTT:      r.maxRTT,
		History:     reconstructHistory(r.rttHistory, r.historyIdx, r.historyLen),
		OpenCount:   r.OpenCount,
		ClosedCount: r.ClosedCount,
		LastChange:  r.LastChange,
	}
}

// TargetStats holds the statistics for a single ping target.
type TargetStats struct {
	Host             string
	IP               string
	ASN              string
	Country          string
	Org              string
	DNSServer        string
	IfaceMTU         int
	PMTU             int
	PMTUBottleneckIP string
	TraceHops        []string
	PortResults      []*PortCheckResult
	mtrStats         *MTRStats
	Sent             int
	Recv             int
	Loss             int
	LastRTT          time.Duration
	MinRTT           time.Duration
	MaxRTT           time.Duration
	SumRTT           time.Duration
	LastTTL          int
	LastLossTime     time.Time
	LastError        string

	// Jitter (RFC 1889)
	jitter int64 // Stored as nanoseconds for smooth calculation

	// History for Sparkline (ring buffer)
	rttHistory []time.Duration
	historyIdx int // write pointer (next slot to write)
	historyLen int // number of valid entries written (up to historySize)

	mu sync.RWMutex
}

// PortCheckView is a read-only snapshot of a single port check result.
type PortCheckView struct {
	Port        int
	Protocol    string
	Status      string
	RTT         time.Duration // Last RTT value
	MinRTT      time.Duration
	AvgRTT      time.Duration
	MaxRTT      time.Duration
	History     []time.Duration // ordered ring buffer snapshot for RTT graph
	OpenCount   int
	ClosedCount int
	LastChange  time.Time
}

// reconstructHistory rebuilds an ordered slice from a ring buffer.
// buf is the raw buffer, idx is the write pointer, length is the count of valid entries.
func reconstructHistory(buf []time.Duration, idx, length int) []time.Duration {
	if length == 0 || buf == nil {
		return nil
	}
	size := len(buf)
	if length < size {
		out := make([]time.Duration, length)
		copy(out, buf[:length])
		return out
	}
	start := idx % size
	out := make([]time.Duration, size)
	copy(out, buf[start:])
	copy(out[size-start:], buf[:start])
	return out
}

// TargetView represents a read-only snapshot of the stats for UI rendering.
type TargetView struct {
	Host             string
	IP               string
	ASN              string
	Country          string
	Org              string
	DNSServer        string
	IfaceMTU         int
	PMTU             int
	PMTUBottleneckIP string
	TraceHops        []string
	PortResults      []PortCheckView
	MTRHops          []HopView
	MTRFlapCount     int
	MTRLastFlapAt    time.Time
	MTRLastFlapDesc  string
	Sent             int
	Recv             int
	Loss             int
	LastRTT          time.Duration
	MinRTT           time.Duration
	MaxRTT           time.Duration
	AvgRTT           time.Duration // Calculated
	Jitter           time.Duration // From RFC 1889 state
	History          []time.Duration
	LastTTL          int
	LastLossTime     time.Time
	LastError        string
}

func NewTargetStats(host string) *TargetStats {
	return &TargetStats{
		Host:       host,
		rttHistory: make([]time.Duration, historySize),
	}
}

// GetView returns a thread-safe snapshot of the current statistics.
func (t *TargetStats) GetView() TargetView {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var avg time.Duration
	if t.Recv > 0 {
		avg = t.SumRTT / time.Duration(t.Recv)
	}

	histCopy := reconstructHistory(t.rttHistory, t.historyIdx, t.historyLen)
	traceCopy := make([]string, len(t.TraceHops))
	copy(traceCopy, t.TraceHops)
	portCopy := make([]PortCheckView, len(t.PortResults))
	for i, r := range t.PortResults {
		portCopy[i] = r.GetView()
	}

	// MTR snapshot is taken outside TargetStats.mu to avoid lock ordering issues;
	// MTRStats carries its own lock.
	var mtrHops []HopView
	var mtrFlapCount int
	var mtrLastFlapAt time.Time
	var mtrLastFlapDesc string
	if t.mtrStats != nil {
		mtrHops = t.mtrStats.View()
		mtrFlapCount, mtrLastFlapAt, mtrLastFlapDesc = t.mtrStats.FlapInfo()
	}

	return TargetView{
		Host:             t.Host,
		IP:               t.IP,
		ASN:              t.ASN,
		Country:          t.Country,
		Org:              t.Org,
		DNSServer:        t.DNSServer,
		IfaceMTU:         t.IfaceMTU,
		PMTU:             t.PMTU,
		PMTUBottleneckIP: t.PMTUBottleneckIP,
		TraceHops:        traceCopy,
		PortResults:      portCopy,
		MTRHops:          mtrHops,
		MTRFlapCount:     mtrFlapCount,
		MTRLastFlapAt:    mtrLastFlapAt,
		MTRLastFlapDesc:  mtrLastFlapDesc,
		Sent:             t.Sent,
		Recv:             t.Recv,
		Loss:             t.Loss,
		LastRTT:          t.LastRTT,
		MinRTT:           t.MinRTT,
		MaxRTT:           t.MaxRTT,
		AvgRTT:           avg,
		Jitter:           time.Duration(t.jitter),
		History:          histCopy,
		LastTTL:          t.LastTTL,
		LastLossTime:     t.LastLossTime,
		LastError:        t.LastError,
	}
}

// MTR returns the MTRStats for this target, creating it lazily on first call.
func (t *TargetStats) MTR() *MTRStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mtrStats == nil {
		t.mtrStats = NewMTRStats()
	}
	return t.mtrStats
}

func (t *TargetStats) SetIP(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.IP = ip
}

func (t *TargetStats) SetASNInfo(number, country, org string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ASN = number
	t.Country = country
	t.Org = org
}

func (t *TargetStats) SetDNSServer(dnsServer string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.DNSServer = dnsServer
}

func (t *TargetStats) SetTraceHops(hops []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.TraceHops = make([]string, len(hops))
	copy(t.TraceHops, hops)
}

func (t *TargetStats) SetIfaceMTU(mtu int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.IfaceMTU = mtu
}

func (t *TargetStats) SetPMTU(pmtu int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.PMTU = pmtu
}

func (t *TargetStats) SetPMTUBottleneckIP(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.PMTUBottleneckIP = ip
}

// SetPortResults replaces the port check results slice atomically.
func (t *TargetStats) SetPortResults(results []*PortCheckResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.PortResults = results
}

func (t *TargetStats) IncSent() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Sent++
}

func (t *TargetStats) OnSuccess(rtt time.Duration, ttl int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// RFC 1889 Jitter Calculation
	// J = J + (|D| - J) / 16
	if t.Recv > 0 {
		delta := int64(rtt - t.LastRTT)
		if delta < 0 {
			delta = -delta
		}
		// t.jitter is stored in nanoseconds
		t.jitter += (delta - t.jitter) / jitterDivisor
	}

	t.Recv++
	t.LastRTT = rtt
	t.LastTTL = ttl
	t.SumRTT += rtt

	if t.MinRTT == 0 || rtt < t.MinRTT {
		t.MinRTT = rtt
	}
	if rtt > t.MaxRTT {
		t.MaxRTT = rtt
	}

	t.appendHistory(rtt)

	t.LastError = ""
}

func (t *TargetStats) appendHistory(rtt time.Duration) {
	t.rttHistory[t.historyIdx%historySize] = rtt
	t.historyIdx++
	if t.historyLen < historySize {
		t.historyLen++
	}
}

func (t *TargetStats) OnFailure(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Loss++
	t.LastLossTime = time.Now()
	t.LastError = reason
	t.appendHistory(0)
}

func (t *TargetStats) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Sent = 0
	t.Recv = 0
	t.Loss = 0
	t.LastRTT = 0
	t.MinRTT = 0
	t.MaxRTT = 0
	t.SumRTT = 0
	t.LastTTL = 0
	t.LastLossTime = time.Time{}
	t.LastError = ""
	t.rttHistory = make([]time.Duration, historySize)
	t.historyIdx = 0
	t.historyLen = 0
	if t.mtrStats != nil {
		t.mtrStats.Reset()
	}
}
