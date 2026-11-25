package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	httpStatusColW = 12 // " Checking..."
	httpCodeColW   = 6  // "  200 "
	httpCountColW  = 7  // " 99999 "
	httpLatColW    = 10 // " 123.45ms " — hundreds to 2 decimal places in ms
	httpSinceColW  = 12 // " 1h23m45s   "
	minHTTPURLW    = 20
)

// httpStatusColorTag returns a tview color tag for an HTTP check status.
func httpStatusColorTag(status string) string {
	return levelTags{crit: "[red::b]", warn: "[darkgray]", ok: "[green]", other: "[darkgray]"}.of(stats.HTTPStatusLevel(status))
}

// httpCodeStr formats a status code for display.
func httpCodeStr(code int) string {
	if code == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", code)
}

// formatHTTPRTT formats a duration as milliseconds with 2 decimal places (e.g. "123.45ms").
func formatHTTPRTT(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
}

// httpSinceStr formats the time elapsed since a status change.
func httpSinceStr(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t).Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

// renderHTTPMonitorTable builds the HTTP Monitor pane string.
// It also detects status changes and appends log messages when log is non-nil.
func renderHTTPMonitorTable(results []*stats.HTTPCheckResult, availW int, lastStatuses map[string]string, log func(string)) string {
	views := make([]stats.HTTPCheckView, len(results))
	for i, r := range results {
		views[i] = r.GetView()
	}

	// Detect status changes and log them.
	if lastStatuses != nil && log != nil {
		for _, v := range views {
			subject := func() string { return fmt.Sprintf("[white]HTTP %s:[white]", tview.Escape(v.URL)) }
			logStatusChangeIfNeeded(lastStatuses, v.URL, v.Status, "Up", subject, log)
		}
	}

	// Compact: URL | Status | Code | Last | Up | Down
	// Full:    URL | Status | Code | Last | Min | Avg | Max | Up | Down | Since
	rtt := func(get func(stats.HTTPCheckView) time.Duration) func(stats.HTTPCheckView) boxCell {
		return plainCell(func(v stats.HTTPCheckView) string { return formatHTTPRTT(get(v)) })
	}
	all := []monitorColumn[stats.HTTPCheckView]{
		{boxColumn{header: "URL", width: minHTTPURLW}, plainCell(func(v stats.HTTPCheckView) string { return tview.Escape(v.URL) }), false},
		{boxColumn{header: "Status", width: httpStatusColW}, func(v stats.HTTPCheckView) boxCell {
			return boxCell{text: v.Status, tag: httpStatusColorTag(v.Status)}
		}, false},
		{boxColumn{header: "Code", width: httpCodeColW}, func(v stats.HTTPCheckView) boxCell {
			return boxCell{text: httpCodeStr(v.StatusCode), tag: httpStatusColorTag(v.Status)}
		}, false},
		{boxColumn{header: "Last", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.RTT }), false},
		{boxColumn{header: "Min", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.MinRTT }), true},
		{boxColumn{header: "Avg", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.AvgRTT }), true},
		{boxColumn{header: "Max", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.MaxRTT }), true},
		{boxColumn{header: "Up", width: httpCountColW}, plainCell(func(v stats.HTTPCheckView) string { return fmt.Sprintf("%d", v.UpCount) }), false},
		{boxColumn{header: "Down", width: httpCountColW}, plainCell(func(v stats.HTTPCheckView) string { return fmt.Sprintf("%d", v.DownCount) }), false},
		{boxColumn{header: "Since", width: httpSinceColW}, plainCell(func(v stats.HTTPCheckView) string { return httpSinceStr(v.LastChange) }), true},
	}
	cols, boxCols := fitMonitorColumns(all, "URL", availW)

	var groups [][][]boxCell
	for _, v := range views {
		groups = append(groups, [][]boxCell{rowCells(cols, v)})
	}

	var sb strings.Builder
	writeLabelledBoxTable(&sb, boxCols, "", groups, " Waiting for results...")
	for _, v := range views {
		if v.Reason != "" {
			fmt.Fprintf(&sb, "\n%s: %s", tview.Escape(v.URL), tview.Escape(v.Reason))
		}
	}
	return sb.String()
}
