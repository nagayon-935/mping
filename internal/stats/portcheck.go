package stats

import (
	"sync"
	"time"
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

	// RTT statistics and ring buffer for Open responses only.
	rtt rttAccumulator

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
	StdDev      time.Duration   // Population stddev, via Welford's online algorithm
	History     []time.Duration // ordered ring buffer snapshot for RTT graph
	OpenCount   int
	ClosedCount int
	LastChange  time.Time
}

func (r *PortCheckResult) SetResult(status string, rtt time.Duration) {
	defer bumpGeneration()
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
	r.rtt.record(rtt)
	r.rtt.appendHistory(rtt, historySize)
}

// CurrentStatus returns the current port status string without copying the
// RTT history ring buffer.
func (r *PortCheckResult) CurrentStatus() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Status
}

// GetView returns a thread-safe snapshot including RTT statistics and history.
func (r *PortCheckResult) GetView() PortCheckView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return PortCheckView{
		Port:        r.Port,
		Protocol:    r.Protocol,
		Status:      r.Status,
		RTT:         r.RTT,
		MinRTT:      r.rtt.min,
		AvgRTT:      r.rtt.avg(),
		MaxRTT:      r.rtt.max,
		StdDev:      r.rtt.stddev(),
		History:     r.rtt.historySnapshot(),
		OpenCount:   r.OpenCount,
		ClosedCount: r.ClosedCount,
		LastChange:  r.LastChange,
	}
}
