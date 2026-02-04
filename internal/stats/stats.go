package stats

import (
	"sync"
	"time"
)

const historySize = 3000

// TargetStats holds the statistics for a single ping target.

type TargetStats struct {

	Host         string

	IP           string

	IfaceMTU     int

	PMTU         int

	TraceHops    []string

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



	// History for Sparkline

	rttHistory []time.Duration

	historyIdx int



	mu sync.RWMutex

}



// TargetView represents a read-only snapshot of the stats for UI rendering.

type TargetView struct {

	Host         string

	IP           string

	IfaceMTU     int

	PMTU         int

	TraceHops    []string

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

		rttHistory: make([]time.Duration, 0, historySize),

	}

}



// GetView returns a thread-safe snapshot of the current statistics.

func (t *TargetStats) GetView() TargetView {

	t.mu.RLock()

	defer t.mu.RUnlock()



	avg := time.Duration(0)



	if t.Recv > 0 {

		avg = t.SumRTT / time.Duration(t.Recv)

	}



	// Copy history for view

	histCopy := make([]time.Duration, len(t.rttHistory))

	copy(histCopy, t.rttHistory)

	traceCopy := make([]string, len(t.TraceHops))

	copy(traceCopy, t.TraceHops)



	return TargetView{

		Host:         t.Host,

		IP:           t.IP,

		IfaceMTU:     t.IfaceMTU,

		PMTU:         t.PMTU,

		TraceHops:    traceCopy,

		Sent:         t.Sent,

		Recv:         t.Recv,

		Loss:         t.Loss,

		LastRTT:      t.LastRTT,

		MinRTT:       t.MinRTT,

		MaxRTT:       t.MaxRTT,

		AvgRTT:       avg,

		Jitter:       time.Duration(t.jitter),

		History:      histCopy,

		LastTTL:      t.LastTTL,

		LastLossTime: t.LastLossTime,

		LastError:    t.LastError,

	}

}

func (t *TargetStats) SetIP(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.IP = ip
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
		t.jitter += (delta - t.jitter) / 16
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

	// Update history ring buffer style (append until full, then shift? or ring?)
	// Simple append and shift is easier for slice
	if len(t.rttHistory) < historySize {
		t.rttHistory = append(t.rttHistory, rtt)
	} else {
		// Shift
		copy(t.rttHistory, t.rttHistory[1:])
		t.rttHistory[historySize-1] = rtt
	}

	t.LastError = ""
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
	t.rttHistory = make([]time.Duration, 0, historySize)
}
