package stats

import (
	"time"
)

// TargetView represents a read-only snapshot of the stats for UI rendering.
type TargetView struct {
	Health           Health
	MeasurementState string
	ID               uint64
	StartedAt        time.Time
	WindowStartedAt  time.Time
	DSCP             string
	IPHistory        []IPChange
	IPChanges        int
	Cancelled        int

	Host             string
	IP               string
	ASN              string
	Country          string
	Org              string
	PTR              string // reverse DNS (PTR) hostname for IP, when --ptr is enabled
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
	Duplicates       int
	LateReplies      int
	LastRTT          time.Duration
	MinRTT           time.Duration
	MaxRTT           time.Duration
	AvgRTT           time.Duration // Calculated
	StdDev           time.Duration // Population stddev, via Welford's online algorithm
	Jitter           time.Duration // From RFC 1889 state
	History          []time.Duration
	LastTTL          int
	LastDSCP         int
	LastLossTime     time.Time
	LastError        string
}

// viewLocked builds a TargetView, obtaining the History field from
// historyFn. Caller must already hold t.mu (a read lock is sufficient).
// Shared by GetView (full history) and GetViewWindow (trailing window
// only) so the two stay in sync automatically.
func (t *TargetStats) viewLocked(historyFn func() []time.Duration) TargetView {
	var avg time.Duration
	if t.Recv > 0 {
		avg = t.rtt.sum / time.Duration(t.Recv)
	}

	histCopy := historyFn()
	traceCopy := make([]string, len(t.TraceHops))
	copy(traceCopy, t.TraceHops)
	portCopy := make([]PortCheckView, len(t.PortResults))
	for i, r := range t.PortResults {
		portCopy[i] = r.GetView()
	}

	// MTRStats carries its own lock; snapshot it while the target reference
	// is protected by TargetStats.mu.
	var mtrHops []HopView
	var mtrFlapCount int
	var mtrLastFlapAt time.Time
	var mtrLastFlapDesc string
	if t.mtrStats != nil {
		mtrHops = t.mtrStats.View()
		mtrFlapCount, mtrLastFlapAt, mtrLastFlapDesc = t.mtrStats.FlapInfo()
	}

	return TargetView{
		Health: t.healthViewLocked(), MeasurementState: t.measurementState,
		DSCP: t.DSCP, IPHistory: append([]IPChange(nil), t.IPHistory...), IPChanges: t.IPChanges,
		ID:               t.ID,
		StartedAt:        t.StartedAt,
		WindowStartedAt:  t.WindowStartedAt,
		Cancelled:        t.Cancelled,
		Host:             t.displayNameLocked(),
		IP:               t.IP,
		ASN:              t.ASN,
		Country:          t.Country,
		Org:              t.Org,
		PTR:              t.PTR,
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
		Duplicates:       t.Duplicates,
		LateReplies:      t.LateReplies,
		LastRTT:          t.LastRTT,
		MinRTT:           t.rtt.min,
		MaxRTT:           t.rtt.max,
		AvgRTT:           avg,
		StdDev:           t.rtt.stddev(),
		Jitter:           time.Duration(t.jitter),
		History:          histCopy,
		LastTTL:          t.LastTTL,
		LastDSCP:         t.LastDSCP,
		LastLossTime:     t.LastLossTime,
		LastError:        t.LastError,
	}
}

// GetView returns a thread-safe snapshot of the current statistics,
// including the full RTT history ring (up to historySize entries).
func (t *TargetStats) GetView() TargetView {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.viewLocked(t.rtt.historySnapshot)
}

// GetViewWindow is like GetView, but History only contains the trailing n
// entries instead of the full ring (see rttAccumulator.historySnapshotWindow).
// Prefer this over GetView for callers that only ever display a fixed
// trailing window regardless of how large historySize is — e.g. the RTT
// graph, which only ever plots a ~30s window of points.
func (t *TargetStats) GetViewWindow(n int) TargetView {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.viewLocked(func() []time.Duration {
		return t.rtt.historySnapshotWindow(n)
	})
}
