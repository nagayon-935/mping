package stats

import (
	"time"
)

// TargetView represents a read-only snapshot of the stats for UI rendering.
type TargetView struct {
	Health Health
	// Thresholds are the ones this target is judged by (its health policy's),
	// and Levels classify the latest statistics against them, so every
	// presentation colours the same values the same way.
	Thresholds       Thresholds
	Levels           Levels
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
// Shared by GetView (full history), GetViewWindow (trailing window only) and
// ReportData (full history plus events) so they stay in sync automatically.
func (t *TargetStats) viewLocked(historyFn func() []time.Duration) TargetView {
	var avg time.Duration
	if t.recv > 0 {
		avg = t.rtt.sum / time.Duration(t.recv)
	}

	histCopy := historyFn()
	traceCopy := make([]string, len(t.traceHops))
	copy(traceCopy, t.traceHops)
	portCopy := make([]PortCheckView, len(t.portResults))
	for i, r := range t.portResults {
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

	th := t.healthPolicyLocked().Thresholds
	return TargetView{
		Health: t.healthViewLocked(), MeasurementState: t.measurementState,
		Thresholds: th, Levels: th.levels(t.recv, t.loss, t.lastRTT, time.Duration(t.jitter)),
		DSCP: t.dscp, IPHistory: append([]IPChange(nil), t.ipHistory...), IPChanges: t.ipChanges,
		ID:               t.id,
		StartedAt:        t.startedAt,
		WindowStartedAt:  t.windowStartedAt,
		Cancelled:        t.cancelled,
		Host:             t.displayNameLocked(),
		IP:               t.ip,
		ASN:              t.asn,
		Country:          t.country,
		Org:              t.org,
		PTR:              t.ptr,
		DNSServer:        t.dnsServer,
		IfaceMTU:         t.ifaceMTU,
		PMTU:             t.pmtu,
		PMTUBottleneckIP: t.pmtuBottleneckIP,
		TraceHops:        traceCopy,
		PortResults:      portCopy,
		MTRHops:          mtrHops,
		MTRFlapCount:     mtrFlapCount,
		MTRLastFlapAt:    mtrLastFlapAt,
		MTRLastFlapDesc:  mtrLastFlapDesc,
		Sent:             t.sent,
		Recv:             t.recv,
		Loss:             t.loss,
		Duplicates:       t.duplicates,
		LateReplies:      t.lateReplies,
		LastRTT:          t.lastRTT,
		MinRTT:           t.rtt.min,
		MaxRTT:           t.rtt.max,
		AvgRTT:           avg,
		StdDev:           t.rtt.stddev(),
		Jitter:           time.Duration(t.jitter),
		History:          histCopy,
		LastTTL:          t.lastTTL,
		LastDSCP:         t.lastDSCP,
		LastLossTime:     t.lastLossTime,
		LastError:        t.lastError,
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
