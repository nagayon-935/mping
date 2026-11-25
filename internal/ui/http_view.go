package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	httpStatusColW = len(" Checking... ")
	httpCodeColW   = 6 // "  200 "
	httpCountColW  = 7 // " 99999 "
	minHTTPURLW    = 20
	maxHTTPURLMinW = 40 // the URL column asks for at most this much before optional columns
)

// httpCodeStr formats a status code for display.
func httpCodeStr(code int) string {
	if code == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", code)
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
			logStatusChangeIfNeeded(lastStatuses, v.URL, v.Status, httpStatusColorTag, subject, log)
		}
	}

	// Compact: URL | Status | Code | Last | Up | Down
	// Full:    URL | Status | Code | Last | Min | Avg | Max | Up | Down | Since
	rtt := func(get func(stats.HTTPCheckView) time.Duration) func(stats.HTTPCheckView) boxCell {
		return plainCell(func(v stats.HTTPCheckView) string { return formatFixedMs(get(v)) })
	}
	all := []monitorColumn[stats.HTTPCheckView]{
		{boxColumn{header: "URL", width: httpURLMinWidth(views)}, plainCell(func(v stats.HTTPCheckView) string { return v.URL }), false},
		{boxColumn{header: "Status", width: httpStatusColW}, func(v stats.HTTPCheckView) boxCell {
			return boxCell{text: v.Status, tag: httpStatusColorTag(v.Status)}
		}, false},
		{boxColumn{header: "Code", width: httpCodeColW, rightAlign: true}, func(v stats.HTTPCheckView) boxCell {
			return boxCell{text: httpCodeStr(v.StatusCode), tag: httpStatusColorTag(v.Status)}
		}, false},
		{boxColumn{header: "Last", width: fixedMsColW, rightAlign: true}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.RTT }), false},
		{boxColumn{header: "Min", width: fixedMsColW, rightAlign: true}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.MinRTT }), true},
		{boxColumn{header: "Avg", width: fixedMsColW, rightAlign: true}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.AvgRTT }), true},
		{boxColumn{header: "Max", width: fixedMsColW, rightAlign: true}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.MaxRTT }), true},
		{boxColumn{header: "Up", width: httpCountColW, rightAlign: true}, plainCell(func(v stats.HTTPCheckView) string { return fmt.Sprintf("%d", v.UpCount) }), false},
		{boxColumn{header: "Down", width: httpCountColW, rightAlign: true}, plainCell(func(v stats.HTTPCheckView) string { return fmt.Sprintf("%d", v.DownCount) }), false},
		{boxColumn{header: "Since", width: timeAgoColW}, plainCell(func(v stats.HTTPCheckView) string { return formatLossAgo(v.LastChange) }), true},
	}
	cols, boxCols := fitMonitorColumns(all, "URL", availW)
	if len(cols) == 0 {
		return truncateToDisplayWidth(monitorTooNarrow, availW)
	}

	var groups [][][]boxCell
	for _, v := range views {
		groups = append(groups, [][]boxCell{rowCells(cols, v)})
	}

	var sb strings.Builder
	writeBoxTable(&sb, boxCols, groups, "Waiting for results...")
	for _, v := range views {
		if v.Reason != "" {
			fmt.Fprintf(&sb, "\n%s: %s", tview.Escape(v.URL), tview.Escape(v.Reason))
		}
	}
	return sb.String()
}

// httpURLMinWidth is the URL column's declared width: the longest URL with
// padding, between minHTTPURLW and maxHTTPURLMinW. URLs are fixed for the run,
// so this keeps them readable without letting the layout follow live values.
func httpURLMinWidth(views []stats.HTTPCheckView) int {
	w := minHTTPURLW
	for _, v := range views {
		w = max(w, runewidth.StringWidth(v.URL)+2)
	}
	return min(w, maxHTTPURLMinW)
}
