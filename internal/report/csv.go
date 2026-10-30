package report

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"time"
)

// csvHeader names the columns of a CSV snapshot. Every row is one target:
// the active targets first, then removed ones (removed_at set). Ports, HTTP
// checks, hops and events are only in the JSON format.
var csvHeader = []string{
	"captured_at", "target_id", "host", "ip", "group", "removed_at",
	"measurement_state", "health_status", "health_reason",
	"sent", "received", "lost", "loss_pct", "recent_loss_pct", "duplicates", "late_replies",
	"last_rtt_ms", "min_rtt_ms", "avg_rtt_ms", "max_rtt_ms", "jitter_ms", "last_ttl",
	"last_error", "last_loss_at", "last_reply_at", "started_at", "statistics_since",
	"asn", "org", "ptr", "route_changes",
}

// CSV renders the report as one row per target. It starts with a UTF-8 byte
// order mark so spreadsheet applications read non-ASCII names correctly.
func (r Report) CSV() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("\ufeff")
	w := csv.NewWriter(&b)
	_ = w.Write(csvHeader)
	for _, t := range append(append([]Target(nil), r.Targets...), r.RemovedTargets...) {
		_ = w.Write(csvRow(r.CapturedAt, t))
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

func csvRow(captured time.Time, t Target) []string {
	v := t.Statistics
	// RTT columns stay empty until a reply arrives, rather than reading 0.
	rtt := func(ms float64) string {
		if v.Recv == 0 {
			return ""
		}
		return strconv.FormatFloat(ms, 'f', 3, 64)
	}
	ttl := ""
	if v.Recv > 0 {
		ttl = strconv.Itoa(v.LastTTL)
	}
	var removed, lastLoss time.Time
	if t.RemovedAt != nil {
		removed = *t.RemovedAt
	}
	if v.LastLossTime != nil {
		lastLoss = *v.LastLossTime
	}
	return []string{
		csvTime(captured), strconv.FormatUint(v.ID, 10), spreadsheetText(v.Host), v.IP, spreadsheetText(t.Group), csvTime(removed),
		v.MeasurementState, v.Health.Status, spreadsheetText(v.Health.Reason),
		strconv.Itoa(v.Sent), strconv.Itoa(v.Recv), strconv.Itoa(v.Loss),
		strconv.FormatFloat(v.LossRatePct, 'f', 1, 64), strconv.FormatFloat(v.Health.RecentLossPct, 'f', 1, 64),
		strconv.Itoa(v.Duplicates), strconv.Itoa(v.LateReplies),
		rtt(v.LastRTTMs), rtt(v.MinRTTMs), rtt(v.AvgRTTMs), rtt(v.MaxRTTMs), rtt(v.JitterMs), ttl,
		spreadsheetText(v.LastError), csvTime(lastLoss), csvTime(v.Health.LastReplyAt), csvTime(v.StartedAt), csvTime(t.WindowStartedAt),
		spreadsheetText(v.ASN), spreadsheetText(v.Org), spreadsheetText(v.PTR), strconv.Itoa(t.MTRFlapCount),
	}
}

func csvTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// spreadsheetText keeps a text cell from being evaluated as a formula when
// the file is opened in a spreadsheet: names and error texts can come from
// the network (PTR records, AS registries).
func spreadsheetText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
