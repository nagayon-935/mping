package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
	fmt.Fprintln(&b, "\nCollection restarts when a YAML change rebuilds probe/network settings; live metadata and membership reloads retain measurements. Ping statistics windows restart on manual reset. Port and HTTP counters can retain earlier results when reset while stopped. Targets and auxiliary checks are captured sequentially within the capture interval. Events and removed targets have bounded retention; this is a statistics snapshot, not a complete packet history. All *_ms fields are milliseconds.")
	return b.String()
}

func writeTarget(b *strings.Builder, t Target) {
	v := t.Statistics
	fmt.Fprintf(b, "\n%s (%s) | Target #%d | Group %s\nStarted: %s | Ping statistics since: %s\n", v.Host, v.IP, v.ID, t.Group, v.StartedAt.Format(time.RFC3339), t.WindowStartedAt.Format(time.RFC3339))
	if t.RemovedAt != nil {
		fmt.Fprintf(b, "Removed: %s\n", t.RemovedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(b, "Sent %d | Received %d | Loss %d | Cancelled %d | Loss/sent %.1f%% | DUP %d | Late %d\nRTT last/min/avg/max %.3f/%.3f/%.3f/%.3f ms | Jitter %.3f ms | TTL %d\nError: %s | ASN: %s %s | PTR: %s | PMTU payload: %d\n", v.Sent, v.Recv, v.Loss, v.Cancelled, v.LossRatePct, v.Duplicates, v.LateReplies, v.LastRTTMs, v.MinRTTMs, v.AvgRTTMs, v.MaxRTTMs, v.JitterMs, v.LastTTL, v.LastError, v.ASN, v.Org, v.PTR, v.PMTU)
	fmt.Fprintf(b, "Interface MTU: %d | DNS server: %s\n", t.InterfaceMTU, t.DNSServer)
	if v.Health.Configured {
		fmt.Fprintf(b, "Measurements: %s | Health: %s — %s | Recent loss (%.0fs): %.1f%%\nLast probe: %s | Last reply: %s | Consecutive failures / replies: %d / %d\n", v.MeasurementState, v.Health.Status, v.Health.Reason, v.Health.WindowSeconds, v.Health.RecentLossPct, v.Health.LastProbeAt.Format(time.RFC3339), v.Health.LastReplyAt.Format(time.RFC3339), v.Health.ConsecutiveFailures, v.Health.ConsecutiveSuccesses)
	}
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
		if event.Count > 1 {
			fmt.Fprintf(b, "  Repeated %d times; last %s\n", event.Count, event.LastAt.Format(time.RFC3339))
		}
	}
	if t.EventsDropped > 0 {
		fmt.Fprintf(b, "%d older target events omitted.\n", t.EventsDropped)
	}
}
