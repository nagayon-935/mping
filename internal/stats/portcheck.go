package stats

import (
	"sync"
	"time"
)

// PortCheckResult holds the result of a single TCP/UDP port check. Its fields
// are guarded by mu; read them through GetView.
type PortCheckResult struct {
	port        int
	protocol    string // "tcp" or "udp"
	status      string // "Open", "Closed", "Filtered", "Open|Filtered", "Error"
	lastRTT     time.Duration
	openCount   int
	closedCount int
	lastChange  time.Time

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

// NewPortCheckResult returns the result of a port check that has not run yet.
func NewPortCheckResult(port int, protocol string) *PortCheckResult {
	return &PortCheckResult{port: port, protocol: protocol, status: "Checking..."}
}

func (r *PortCheckResult) SetResult(status string, rtt time.Duration) {
	defer bumpGeneration()
	r.mu.Lock()
	defer r.mu.Unlock()
	if status != r.status {
		r.lastChange = time.Now()
	}
	r.status = status
	r.lastRTT = rtt
	switch status {
	case "Open":
		r.openCount++
		r.recordRTT(rtt)
	case "Closed", "Filtered", "Open|Filtered":
		r.closedCount++
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
	return r.status
}

// GetView returns a thread-safe snapshot including RTT statistics and history.
func (r *PortCheckResult) GetView() PortCheckView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return PortCheckView{
		Port:        r.port,
		Protocol:    r.protocol,
		Status:      r.status,
		RTT:         r.lastRTT,
		MinRTT:      r.rtt.min,
		AvgRTT:      r.rtt.avg(),
		MaxRTT:      r.rtt.max,
		StdDev:      r.rtt.stddev(),
		History:     r.rtt.historySnapshot(),
		OpenCount:   r.openCount,
		ClosedCount: r.closedCount,
		LastChange:  r.lastChange,
	}
}
