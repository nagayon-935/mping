package stats

import "time"

// ExportSnapshot is the root of the JSON statistics export.
type ExportSnapshot struct {
	Timestamp  time.Time          `json:"timestamp"`
	Targets    []TargetSummary    `json:"targets"`
	HTTPChecks []HTTPCheckSummary `json:"http_checks,omitempty"`
}

// TargetSummary is a JSON-serialisable summary for a single ping target.
type TargetSummary struct {
	Host    string `json:"host"`
	IP      string `json:"ip"`
	ASN     string `json:"asn,omitempty"`
	Country string `json:"country,omitempty"`
	Org     string `json:"org,omitempty"`
	PTR     string `json:"ptr,omitempty"`
	Sent    int    `json:"sent"`
	Recv    int    `json:"recv"`
	Loss    int    `json:"loss"`
	// Duplicates and LateReplies are omitempty since the overwhelming
	// majority of runs never see either; see stats.TargetStats' fields of
	// the same name for what each counts and why neither is folded into
	// Recv or Loss.
	Duplicates  int     `json:"duplicates,omitempty"`
	LateReplies int     `json:"late_replies,omitempty"`
	LossRatePct float64 `json:"loss_rate_pct"`
	MinRTTMs    float64 `json:"min_rtt_ms"`
	AvgRTTMs    float64 `json:"avg_rtt_ms"`
	MaxRTTMs    float64 `json:"max_rtt_ms"`
	LastRTTMs   float64 `json:"last_rtt_ms"`
	JitterMs    float64 `json:"jitter_ms"`
	LastTTL     int     `json:"last_ttl"`
	// LastDSCP is the TOS (IPv4) / TrafficClass (IPv6) byte observed on the
	// most recent reply's own IP header (see stats.TargetStats.LastDSCP).
	// Always 0 for IPv4 targets: x/net's ipv4.ControlMessage has no TOS
	// field, so IPv4 has no receive-side DSCP observation at all.
	LastDSCP         int           `json:"last_dscp"`
	LastError        string        `json:"last_error,omitempty"`
	LastLossTime     *time.Time    `json:"last_loss_time,omitempty"`
	TraceHops        []string      `json:"trace_hops,omitempty"`
	PortResults      []PortSummary `json:"port_results,omitempty"`
	PMTU             int           `json:"pmtu,omitempty"`
	PMTUBottleneckIP string        `json:"pmtu_bottleneck_ip,omitempty"`
	MTRHops          []HopSummary  `json:"mtr_hops,omitempty"`
}

// HopSummary is a JSON-serialisable summary for a single MTR hop.
type HopSummary struct {
	TTL       int     `json:"ttl"`
	IP        string  `json:"ip"` // "" when hop never responded
	ASN       string  `json:"asn,omitempty"`
	Country   string  `json:"country,omitempty"`
	Org       string  `json:"org,omitempty"`
	Sent      int     `json:"sent"`
	Recv      int     `json:"recv"`
	LossPct   float64 `json:"loss_pct"`
	LastRTTMs float64 `json:"last_rtt_ms"`
	AvgRTTMs  float64 `json:"avg_rtt_ms"`
	MinRTTMs  float64 `json:"min_rtt_ms"`
	MaxRTTMs  float64 `json:"max_rtt_ms"`
	JitterMs  float64 `json:"jitter_ms"`
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

// HTTPCheckSummary is a JSON-serialisable summary for a single HTTP health check.
type HTTPCheckSummary struct {
	URL        string  `json:"url"`
	Status     string  `json:"status"`
	StatusCode int     `json:"status_code"`
	LastRTTMs  float64 `json:"last_rtt_ms"`
	MinRTTMs   float64 `json:"min_rtt_ms,omitempty"`
	AvgRTTMs   float64 `json:"avg_rtt_ms,omitempty"`
	MaxRTTMs   float64 `json:"max_rtt_ms,omitempty"`
	UpCount    int     `json:"up_count"`
	DownCount  int     `json:"down_count"`
}

// durationMs converts a time.Duration to milliseconds (float64).
func durationMs(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// BuildSnapshot builds an ExportSnapshot from the given targets and optional HTTP results.
// GetView is called once per target for a consistent, thread-safe snapshot.
// If targets is nil or empty, Targets in the returned snapshot is a non-nil,
// empty slice (JSON encodes as [] rather than null).
func BuildSnapshot(targets []*TargetStats, httpResults []*HTTPCheckResult) ExportSnapshot {
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

		var mtrHops []HopSummary
		for _, h := range v.MTRHops {
			mtrHops = append(mtrHops, HopSummary{
				TTL:       h.TTL,
				IP:        h.IP,
				ASN:       h.ASN,
				Country:   h.Country,
				Org:       h.Org,
				Sent:      h.Sent,
				Recv:      h.Recv,
				LossPct:   h.LossPct,
				LastRTTMs: durationMs(h.LastRTT),
				AvgRTTMs:  durationMs(h.AvgRTT),
				MinRTTMs:  durationMs(h.MinRTT),
				MaxRTTMs:  durationMs(h.MaxRTT),
				JitterMs:  durationMs(h.Jitter),
			})
		}

		snap.Targets = append(snap.Targets, TargetSummary{
			Host:             v.Host,
			IP:               v.IP,
			ASN:              v.ASN,
			Country:          v.Country,
			Org:              v.Org,
			PTR:              v.PTR,
			Sent:             v.Sent,
			Recv:             v.Recv,
			Loss:             v.Loss,
			Duplicates:       v.Duplicates,
			LateReplies:      v.LateReplies,
			LossRatePct:      lossRatePct,
			MinRTTMs:         durationMs(v.MinRTT),
			AvgRTTMs:         durationMs(v.AvgRTT),
			MaxRTTMs:         durationMs(v.MaxRTT),
			LastRTTMs:        durationMs(v.LastRTT),
			JitterMs:         durationMs(v.Jitter),
			LastTTL:          v.LastTTL,
			LastDSCP:         v.LastDSCP,
			LastError:        v.LastError,
			LastLossTime:     lastLossTime,
			TraceHops:        traceHops,
			PortResults:      portResults,
			PMTU:             v.PMTU,
			PMTUBottleneckIP: v.PMTUBottleneckIP,
			MTRHops:          mtrHops,
		})
	}

	for _, r := range httpResults {
		v := r.GetView()
		snap.HTTPChecks = append(snap.HTTPChecks, HTTPCheckSummary{
			URL:        v.URL,
			Status:     v.Status,
			StatusCode: v.StatusCode,
			LastRTTMs:  durationMs(v.RTT),
			MinRTTMs:   durationMs(v.MinRTT),
			AvgRTTMs:   durationMs(v.AvgRTT),
			MaxRTTMs:   durationMs(v.MaxRTT),
			UpCount:    v.UpCount,
			DownCount:  v.DownCount,
		})
	}

	return snap
}
