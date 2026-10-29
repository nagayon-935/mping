// Package report formats immutable investigation results independently of TUI.
package report

import (
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

type Settings struct {
	IntervalMS          int64              `json:"interval_ms"`
	TimeoutMS           int64              `json:"timeout_ms"`
	PayloadBytes        int                `json:"payload_bytes"`
	Interface           string             `json:"interface,omitempty"`
	BoundSource         string             `json:"bound_source,omitempty"`
	SourceIPv4Hint      string             `json:"source_ipv4_hint,omitempty"`
	SourceIPv6Hint      string             `json:"source_ipv6_hint,omitempty"`
	Network             string             `json:"network"`
	Count               int                `json:"count"`
	DurationDeadline    *time.Time         `json:"duration_deadline,omitempty"`
	PMTUDiscovery       bool               `json:"pmtu_discovery"`
	Duration            string             `json:"duration"`
	DNSServer           string             `json:"dns_server,omitempty"`
	ResolveAll          bool               `json:"resolve_all"`
	Traceroute          bool               `json:"traceroute"`
	MTR                 bool               `json:"mtr"`
	Ports               []string           `json:"ports,omitempty"`
	HTTPURLs            []string           `json:"http_urls,omitempty"`
	Thresholds          map[string]float64 `json:"thresholds"`
	HealthFailures      int                `json:"health_failures"`
	HealthRecoveries    int                `json:"health_recoveries"`
	HealthWindowSeconds float64            `json:"health_window_seconds"`
	HTTPFollowRedirects bool               `json:"http_follow_redirects"`
}

// Port uses milliseconds throughout, matching the statistics export.
type Port struct {
	Port        int       `json:"port"`
	Protocol    string    `json:"protocol"`
	Status      string    `json:"status"`
	LastRTTMS   float64   `json:"last_rtt_ms"`
	MinRTTMS    float64   `json:"min_rtt_ms"`
	AvgRTTMS    float64   `json:"avg_rtt_ms"`
	MaxRTTMS    float64   `json:"max_rtt_ms"`
	OpenCount   int       `json:"open_count"`
	ClosedCount int       `json:"closed_count"`
	LastChange  time.Time `json:"last_change"`
}

type Target struct {
	Statistics       stats.TargetSummary `json:"statistics"`
	WindowStartedAt  time.Time           `json:"window_started_at"`
	Group            string              `json:"group,omitempty"`
	InterfaceMTU     int                 `json:"interface_mtu,omitempty"`
	DNSServer        string              `json:"dns_server,omitempty"`
	MTRFlapCount     int                 `json:"mtr_flap_count"`
	MTRLastFlapAt    time.Time           `json:"mtr_last_flap_at,omitzero"`
	MTRLastFlapDesc  string              `json:"mtr_last_flap_description,omitempty"`
	IPHistoryDropped int                 `json:"ip_history_dropped"`
	IPChanges        int                 `json:"ip_changes"`
	IPHistory        []stats.IPChange    `json:"ip_history,omitempty"`
	Events           []stats.Event       `json:"events,omitempty"`
	EventsDropped    int                 `json:"events_dropped"`
	RemovedAt        *time.Time          `json:"removed_at,omitempty"`
	PortDetails      []Port              `json:"port_details,omitempty"`
}

// NewTarget reads the target once; all output formats use this same data.
func NewTarget(t *stats.TargetStats) Target {
	v, events, dropped := t.ReportData()
	summary := stats.BuildSnapshotFromViews([]stats.TargetView{v}, nil).Targets[0]
	ports := make([]Port, 0, len(v.PortResults))
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	for _, p := range v.PortResults {
		ports = append(ports, Port{Port: p.Port, Protocol: p.Protocol, Status: p.Status, LastRTTMS: ms(p.RTT), MinRTTMS: ms(p.MinRTT), AvgRTTMS: ms(p.AvgRTT), MaxRTTMS: ms(p.MaxRTT), OpenCount: p.OpenCount, ClosedCount: p.ClosedCount, LastChange: p.LastChange})
	}
	ipDropped := 0
	if len(v.IPHistory) > 0 {
		ipDropped = max(0, v.IPChanges+1-len(v.IPHistory))
	}
	return Target{InterfaceMTU: v.IfaceMTU, DNSServer: v.DNSServer, MTRFlapCount: v.MTRFlapCount, MTRLastFlapAt: v.MTRLastFlapAt, MTRLastFlapDesc: v.MTRLastFlapDesc, IPHistoryDropped: ipDropped, Statistics: summary, WindowStartedAt: v.WindowStartedAt, IPChanges: v.IPChanges, IPHistory: v.IPHistory, Events: events, EventsDropped: dropped, PortDetails: ports}
}

type Report struct {
	SchemaVersion         int                      `json:"schema_version"`
	CollectionStartedAt   time.Time                `json:"collection_started_at"`
	SessionStartedAt      time.Time                `json:"session_started_at"`
	CapturedAt            time.Time                `json:"captured_at"`
	CaptureCompletedAt    time.Time                `json:"capture_completed_at"`
	State                 string                   `json:"state"`
	PingFinished          bool                     `json:"ping_finished"`
	Scope                 string                   `json:"scope"`
	Settings              Settings                 `json:"settings"`
	Targets               []Target                 `json:"targets"`
	RemovedTargets        []Target                 `json:"removed_targets,omitempty"`
	RemovedTargetsDropped int                      `json:"removed_targets_dropped"`
	HTTPChecks            []stats.HTTPCheckSummary `json:"http_checks,omitempty"`
}
