// Package report formats immutable investigation results independently of TUI.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

type Settings struct {
	IntervalMS       int64              `json:"interval_ms"`
	TimeoutMS        int64              `json:"timeout_ms"`
	PayloadBytes     int                `json:"payload_bytes"`
	Interface        string             `json:"interface,omitempty"`
	BoundSource      string             `json:"bound_source,omitempty"`
	SourceIPv4Hint   string             `json:"source_ipv4_hint,omitempty"`
	SourceIPv6Hint   string             `json:"source_ipv6_hint,omitempty"`
	Network          string             `json:"network"`
	Count            int                `json:"count"`
	DurationDeadline *time.Time         `json:"duration_deadline,omitempty"`
	PMTUDiscovery    bool               `json:"pmtu_discovery"`
	Duration         string             `json:"duration"`
	DSCP             string             `json:"dscp,omitempty"`
	DNSServer        string             `json:"dns_server,omitempty"`
	ResolveAll       bool               `json:"resolve_all"`
	Traceroute       bool               `json:"traceroute"`
	MTR              bool               `json:"mtr"`
	Ports            []string           `json:"ports,omitempty"`
	HTTPURLs         []string           `json:"http_urls,omitempty"`
	Thresholds       map[string]float64 `json:"thresholds"`
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
	DSCP             string              `json:"dscp,omitempty"`
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
	return Target{InterfaceMTU: v.IfaceMTU, DNSServer: v.DNSServer, MTRFlapCount: v.MTRFlapCount, MTRLastFlapAt: v.MTRLastFlapAt, MTRLastFlapDesc: v.MTRLastFlapDesc, IPHistoryDropped: ipDropped, Statistics: summary, WindowStartedAt: v.WindowStartedAt, DSCP: v.DSCP, IPChanges: v.IPChanges, IPHistory: v.IPHistory, Events: events, EventsDropped: dropped, PortDetails: ports}
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

func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mping investigation report (schema %d)\nSession started: %s\nCollection started: %s\nCaptured: %s - %s\nState: %s | Ping finished: %t | Scope: %s\n", r.SchemaVersion, r.SessionStartedAt.Format(time.RFC3339), r.CollectionStartedAt.Format(time.RFC3339), r.CapturedAt.Format(time.RFC3339Nano), r.CaptureCompletedAt.Format(time.RFC3339Nano), r.State, r.PingFinished, r.Scope)
	settings, _ := json.MarshalIndent(r.Settings, "", "  ")
	fmt.Fprintf(&b, "\nMeasurement settings:\n%s\n", settings)
	fmt.Fprintln(&b, "\nActive targets:")
	for _, target := range r.Targets {
		writeTarget(&b, target)
	}
	if len(r.RemovedTargets) > 0 {
		fmt.Fprintln(&b, "\nRemoved targets (final measurements):")
		for _, target := range r.RemovedTargets {
			writeTarget(&b, target)
		}
	}
	if r.RemovedTargetsDropped > 0 {
		fmt.Fprintf(&b, "\n%d older removed targets omitted.\n", r.RemovedTargetsDropped)
	}
	if len(r.HTTPChecks) > 0 {
		fmt.Fprintln(&b, "\nIndependent HTTP checks:")
		for _, check := range r.HTTPChecks {
			fmt.Fprintf(&b, "%s | %s | Code %d | Last %.3f ms | Min/Avg/Max %.3f/%.3f/%.3f ms | Up %d Down %d\n", check.URL, check.Status, check.StatusCode, check.LastRTTMs, check.MinRTTMs, check.AvgRTTMs, check.MaxRTTMs, check.UpCount, check.DownCount)
		}
	}
	fmt.Fprintln(&b, "\nCollection restarts on YAML reload. Ping statistics windows restart on manual reset. Port and HTTP counters can retain earlier results when reset while stopped. Targets and auxiliary checks are captured sequentially within the capture interval. Events and removed targets have bounded retention; this is a statistics snapshot, not a complete packet history. All *_ms fields are milliseconds.")
	return b.String()
}

func writeTarget(b *strings.Builder, t Target) {
	v := t.Statistics
	fmt.Fprintf(b, "\n%s (%s) | Target #%d | Group %s | DSCP %s\nStarted: %s | Ping statistics since: %s\n", v.Host, v.IP, v.ID, t.Group, t.DSCP, v.StartedAt.Format(time.RFC3339), t.WindowStartedAt.Format(time.RFC3339))
	if t.RemovedAt != nil {
		fmt.Fprintf(b, "Removed: %s\n", t.RemovedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(b, "Sent %d | Received %d | Loss %d | Cancelled %d | Loss/sent %.1f%% | DUP %d | Late %d\nRTT last/min/avg/max %.3f/%.3f/%.3f/%.3f ms | Jitter %.3f ms | TTL %d\nError: %s | ASN: %s %s | PTR: %s | PMTU payload: %d\n", v.Sent, v.Recv, v.Loss, v.Cancelled, v.LossRatePct, v.Duplicates, v.LateReplies, v.LastRTTMs, v.MinRTTMs, v.AvgRTTMs, v.MaxRTTMs, v.JitterMs, v.LastTTL, v.LastError, v.ASN, v.Org, v.PTR, v.PMTU)
	fmt.Fprintf(b, "Interface MTU: %d | DNS server: %s\n", t.InterfaceMTU, t.DNSServer)
	if t.IPChanges > 0 {
		fmt.Fprintf(b, "Destination IP changed %d times during this measurement.\n", t.IPChanges)
	}
	if t.IPHistoryDropped > 0 {
		fmt.Fprintf(b, "%d older IP history entries omitted.\n", t.IPHistoryDropped)
	}
	if t.MTRFlapCount > 0 {
		fmt.Fprintf(b, "MTR route changes %d | Last %s: %s\n", t.MTRFlapCount, t.MTRLastFlapAt.Format(time.RFC3339), t.MTRLastFlapDesc)
	}
	for _, change := range t.IPHistory {
		fmt.Fprintf(b, "  %s IP %s\n", change.At.Format(time.RFC3339), change.IP)
	}
	if len(v.TraceHops) > 0 {
		fmt.Fprintf(b, "Route: %s\n", strings.Join(v.TraceHops, " -> "))
	}
	for _, hop := range v.MTRHops {
		fmt.Fprintf(b, "MTR hop %d %s | Loss %.1f%% | Sent %d Recv %d | Last/Min/Avg/Max %.3f/%.3f/%.3f/%.3f ms | Jitter %.3f ms\n", hop.TTL, hop.IP, hop.LossPct, hop.Sent, hop.Recv, hop.LastRTTMs, hop.MinRTTMs, hop.AvgRTTMs, hop.MaxRTTMs, hop.JitterMs)
	}
	for _, port := range t.PortDetails {
		fmt.Fprintf(b, "Port %d/%s %s | Last/Min/Avg/Max %.3f/%.3f/%.3f/%.3f ms | Open %d Closed/Filtered %d | Changed %s\n", port.Port, port.Protocol, port.Status, port.LastRTTMS, port.MinRTTMS, port.AvgRTTMS, port.MaxRTTMS, port.OpenCount, port.ClosedCount, port.LastChange.Format(time.RFC3339))
	}
	for _, event := range t.Events {
		fmt.Fprintf(b, "%s %s: %s\n", event.At.Format(time.RFC3339), event.Kind, event.Message)
	}
	if t.EventsDropped > 0 {
		fmt.Fprintf(b, "%d older target events omitted.\n", t.EventsDropped)
	}
}

// Write publishes a complete new file without overwriting an existing path.
// Linking a closed temporary file provides atomic visibility and exclusive
// creation, including concurrent saves and symlink destinations.
func Write(path, format string, r Report) error {
	var data []byte
	var err error
	switch format {
	case "text":
		data = []byte(r.Text())
	case "json":
		data, err = json.MarshalIndent(r, "", "  ")
	default:
		return fmt.Errorf("unsupported report format %q", format)
	}
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mping-report-*")
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write report: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), path); err != nil {
		return fmt.Errorf("save report (choose a new path if it exists): %w", err)
	}
	return nil
}
