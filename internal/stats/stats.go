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
	mu          sync.RWMutex
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
	case "Closed", "Filtered", "Open|Filtered":
		r.ClosedCount++
	}
}

func (r *PortCheckResult) GetResult() (status string, rtt time.Duration, openCount, closedCount int, lastChange time.Time) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Status, r.RTT, r.OpenCount, r.ClosedCount, r.LastChange
}

// TargetStats holds the statistics for a single ping target.
type TargetStats struct {
	Host              string
	IP                string
	ASN               string
	IfaceMTU          int
	PMTU              int
	PMTUBottleneckIP  string
	TraceHops         []string
	PortResults       []*PortCheckResult
	mtrStats          *MTRStats
	Sent         int
	Recv         int
	Loss         int
	LastRTT      time.Duration
	MinRTT       time.Duration
	MaxRTT       time.Duration
	SumRTT       time.Duration
	LastTTL      int
	LastLossTime time.Time
	LastError    string

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
	RTT         time.Duration
	OpenCount   int
	ClosedCount int
	LastChange  time.Time
}

// TargetView represents a read-only snapshot of the stats for UI rendering.
type TargetView struct {
	Host             string
	IP               string
	ASN              string
	IfaceMTU         int
	PMTU             int
	PMTUBottleneckIP string
	TraceHops        []string
	PortResults  []PortCheckView
	MTRHops      []HopView
	Sent         int
	Recv         int
	Loss         int
	LastRTT      time.Duration
	MinRTT       time.Duration
	MaxRTT       time.Duration
	AvgRTT       time.Duration // Calculated
	Jitter       time.Duration // From RFC 1889 state
	History      []time.Duration
	LastTTL      int
	LastLossTime time.Time
	LastError    string
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

	// Reconstruct ordered history from ring buffer
	var histCopy []time.Duration
	if t.historyLen < historySize {
		histCopy = make([]time.Duration, t.historyLen)
		copy(histCopy, t.rttHistory[:t.historyLen])
	} else {
		start := t.historyIdx % historySize
		histCopy = make([]time.Duration, historySize)
		copy(histCopy, t.rttHistory[start:])
		copy(histCopy[historySize-start:], t.rttHistory[:start])
	}
	traceCopy := make([]string, len(t.TraceHops))
	copy(traceCopy, t.TraceHops)
	portCopy := make([]PortCheckView, len(t.PortResults))
	for i, r := range t.PortResults {
		status, rtt, openCount, closedCount, lastChange := r.GetResult()
		portCopy[i] = PortCheckView{
			Port: r.Port, Protocol: r.Protocol,
			Status: status, RTT: rtt,
			OpenCount: openCount, ClosedCount: closedCount, LastChange: lastChange,
		}
	}

	// MTR snapshot is taken outside TargetStats.mu to avoid lock ordering issues;
	// MTRStats carries its own lock.
	var mtrHops []HopView
	if t.mtrStats != nil {
		mtrHops = t.mtrStats.View()
	}

	return TargetView{
		Host:             t.Host,
		IP:               t.IP,
		ASN:              t.ASN,
		IfaceMTU:         t.IfaceMTU,
		PMTU:             t.PMTU,
		PMTUBottleneckIP: t.PMTUBottleneckIP,
		TraceHops:        traceCopy,
		PortResults:      portCopy,
		MTRHops:          mtrHops,
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

func (t *TargetStats) SetASN(asn string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ASN = asn
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
