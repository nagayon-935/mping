package stats

import "time"

// ExportSnapshot is the root of the JSON statistics export.
type ExportSnapshot struct {
	Timestamp time.Time       `json:"timestamp"`
	Targets   []TargetSummary `json:"targets"`
}

// TargetSummary is a JSON-serialisable summary for a single ping target.
type TargetSummary struct {
	Host         string        `json:"host"`
	IP           string        `json:"ip"`
	ASN          string        `json:"asn,omitempty"`
	Sent         int           `json:"sent"`
	Recv         int           `json:"recv"`
	Loss         int           `json:"loss"`
	LossRatePct  float64       `json:"loss_rate_pct"`
	MinRTTMs     float64       `json:"min_rtt_ms"`
	AvgRTTMs     float64       `json:"avg_rtt_ms"`
	MaxRTTMs     float64       `json:"max_rtt_ms"`
	LastRTTMs    float64       `json:"last_rtt_ms"`
	JitterMs     float64       `json:"jitter_ms"`
	LastTTL      int           `json:"last_ttl"`
	LastError    string        `json:"last_error,omitempty"`
	LastLossTime *time.Time    `json:"last_loss_time,omitempty"`
	TraceHops    []string      `json:"trace_hops,omitempty"`
	PortResults  []PortSummary `json:"port_results,omitempty"`
	PMTU         int           `json:"pmtu,omitempty"`
}

// PortSummary is a JSON-serialisable summary for a single port check result.
type PortSummary struct {
	Port        int     `json:"port"`
	Protocol    string  `json:"protocol"`
	Status      string  `json:"status"`
	RTTMs       float64 `json:"rtt_ms"`
	OpenCount   int     `json:"open_count"`
	ClosedCount int     `json:"closed_count"`
}

// durationMs converts a time.Duration to milliseconds (float64).
func durationMs(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// BuildSnapshot builds an ExportSnapshot from the given targets.
// GetView is called once per target for a consistent, thread-safe snapshot.
// If targets is nil or empty, Targets in the returned snapshot is a non-nil,
// empty slice (JSON encodes as [] rather than null).
func BuildSnapshot(targets []*TargetStats) ExportSnapshot {
	snap := ExportSnapshot{
		Timestamp: time.Now().UTC(),
		Targets:   make([]TargetSummary, 0, len(targets)),
	}
	for _, t := range targets {
		v := t.GetView()

		var lossRatePct float64
		if v.Sent > 0 {
			lossRatePct = float64(v.Loss) / float64(v.Sent) * 100
		}

		var lastLossTime *time.Time
		if !v.LastLossTime.IsZero() {
			ts := v.LastLossTime.UTC()
			lastLossTime = &ts
		}

		var portResults []PortSummary
		for _, p := range v.PortResults {
			portResults = append(portResults, PortSummary{
				Port:        p.Port,
				Protocol:    p.Protocol,
				Status:      p.Status,
				RTTMs:       durationMs(p.RTT),
				OpenCount:   p.OpenCount,
				ClosedCount: p.ClosedCount,
			})
		}

		var traceHops []string
		if len(v.TraceHops) > 0 {
			traceHops = v.TraceHops
		}

		snap.Targets = append(snap.Targets, TargetSummary{
			Host:         v.Host,
			IP:           v.IP,
			ASN:          v.ASN,
			Sent:         v.Sent,
			Recv:         v.Recv,
			Loss:         v.Loss,
			LossRatePct:  lossRatePct,
			MinRTTMs:     durationMs(v.MinRTT),
			AvgRTTMs:     durationMs(v.AvgRTT),
			MaxRTTMs:     durationMs(v.MaxRTT),
			LastRTTMs:    durationMs(v.LastRTT),
			JitterMs:     durationMs(v.Jitter),
			LastTTL:      v.LastTTL,
			LastError:    v.LastError,
			LastLossTime: lastLossTime,
			TraceHops:    traceHops,
			PortResults:  portResults,
			PMTU:         v.PMTU,
		})
	}
	return snap
}
