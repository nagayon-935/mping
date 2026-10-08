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
	httpStatusColW = 12 // " Checking... "
	httpCodeColW   = 6  // "  200 "
	httpCountColW  = 7  // " 99999 "
	httpLatColW    = 10 // " 123.45ms " — hundreds to 2 decimal places in ms
	httpSinceColW  = 12 // " 1h23m45s  "
	minHTTPURLW    = 20
)

// httpURLColW returns the URL column width given results, available terminal width,
// and whether compact mode is active. It sizes the column to the longest URL
// (with 2 chars of padding) so it does not waste space, capped at what fits.
func httpURLColW(results []*stats.HTTPCheckResult, availW int, compact bool) int {
	var fixedW int
	if compact {
		// URL + Status + Code + Last + Up + Down + 5 inner separators
		fixedW = httpStatusColW + httpCodeColW + httpLatColW + httpCountColW*2 + 5
	} else {
		// URL + Status + Code + Last + Min + Avg + Max + Up + Down + Since + 9 inner separators
		fixedW = httpStatusColW + httpCodeColW + httpLatColW*4 + httpCountColW*2 + httpSinceColW + 9
	}
	// -2 accounts for the outer left/right border chars (┌─┐ / └─┘).
	cap := availW - fixedW - 2
	if cap < minHTTPURLW {
		cap = minHTTPURLW
	}

	// Fit to the longest URL (+ 2 padding spaces) to avoid wasting space.
	contentW := minHTTPURLW
	for _, r := range results {
		v := r.GetView()
		if w := runewidth.StringWidth(v.URL) + 2; w > contentW {
			contentW = w
		}
	}

	if contentW < cap {
		return contentW
	}
	return cap
}

// httpStatusColorTag returns a tview color tag for an HTTP check status.
func httpStatusColorTag(status string) string {
	switch status {
	case "Up":
		return "[green]"
	case "Down", "Error":
		return "[red::b]"
	default:
		return "[darkgray]"
	}
}

// httpCodeColorTag returns a tview color tag for an HTTP status code.
func httpCodeColorTag(code int) string {
	switch {
	case code == 0:
		return "[darkgray]"
	case code < 300:
		return "[green]"
	case code < 400:
		return "[orange]"
	case code < 500:
		return "[orange]"
	default:
		return "[red::b]"
	}
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
// It also detects status changes and appends log messages when errorLogs/errorView
// are non-nil.
func renderHTTPMonitorTable(results []*stats.HTTPCheckResult, availW int, lastStatuses map[string]string, errorLogs *[]string, errorView *tview.TextView) string {
	// Compact: drop Min/Avg/Max/Since when screen is narrow.
	// +2 accounts for the outer left/right border chars that consume available width.
	fullFixed := minHTTPURLW + httpStatusColW + httpCodeColW + httpLatColW*4 + httpCountColW*2 + httpSinceColW + 9 + 2
	compact := availW < fullFixed

	urlW := httpURLColW(results, availW, compact)

	// Detect status changes and log them.
	if lastStatuses != nil && errorLogs != nil {
		for _, r := range results {
			v := r.GetView()
			subject := fmt.Sprintf("[white]HTTP %s:[white]", tview.Escape(v.URL))
			logStatusChangeIfNeeded(lastStatuses, v.URL, v.Status, "Up", subject, errorLogs, errorView)
		}
	}

	// Compact: URL | Status | Code | Last | Up | Down
	// Full:    URL | Status | Code | Last | Min | Avg | Max | Up | Down | Since
	type httpColumn struct {
		boxColumn
		cell func(stats.HTTPCheckView) boxCell
		full bool // shown only in the full layout
	}
	rtt := func(get func(stats.HTTPCheckView) time.Duration) func(stats.HTTPCheckView) boxCell {
		return func(v stats.HTTPCheckView) boxCell {
			return boxCell{text: formatHTTPRTT(get(v)), tag: mtrRTTColorTag(get(v))}
		}
	}
	plain := func(text func(stats.HTTPCheckView) string) func(stats.HTTPCheckView) boxCell {
		return func(v stats.HTTPCheckView) boxCell { return boxCell{text: text(v)} }
	}
	all := []httpColumn{
		{boxColumn{header: "URL", width: urlW}, plain(func(v stats.HTTPCheckView) string { return tview.Escape(v.URL) }), false},
		{boxColumn{header: "Status", width: httpStatusColW}, func(v stats.HTTPCheckView) boxCell {
			return boxCell{text: v.Status, tag: httpStatusColorTag(v.Status)}
		}, false},
		{boxColumn{header: "Code", width: httpCodeColW}, func(v stats.HTTPCheckView) boxCell {
			return boxCell{text: httpCodeStr(v.StatusCode), tag: httpCodeColorTag(v.StatusCode)}
		}, false},
		{boxColumn{header: "Last", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.RTT }), false},
		{boxColumn{header: "Min", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.MinRTT }), true},
		{boxColumn{header: "Avg", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.AvgRTT }), true},
		{boxColumn{header: "Max", width: httpLatColW}, rtt(func(v stats.HTTPCheckView) time.Duration { return v.MaxRTT }), true},
		{boxColumn{header: "Up", width: httpCountColW}, plain(func(v stats.HTTPCheckView) string { return fmt.Sprintf("%d", v.UpCount) }), false},
		{boxColumn{header: "Down", width: httpCountColW}, plain(func(v stats.HTTPCheckView) string { return fmt.Sprintf("%d", v.DownCount) }), false},
		{boxColumn{header: "Since", width: httpSinceColW}, plain(func(v stats.HTTPCheckView) string { return httpSinceStr(v.LastChange) }), true},
	}
	var cols []httpColumn
	for _, c := range all {
		if !c.full || !compact {
			cols = append(cols, c)
		}
	}
	boxCols := make([]boxColumn, len(cols))
	for i, c := range cols {
		boxCols[i] = c.boxColumn
	}

	var groups [][][]boxCell
	for _, r := range results {
		v := r.GetView()
		cells := make([]boxCell, len(cols))
		for i, c := range cols {
			cells[i] = c.cell(v)
		}
		groups = append(groups, [][]boxCell{cells})
	}

	var sb strings.Builder
	writeLabelledBoxTable(&sb, boxCols, "", groups, " Waiting for results...")
	return sb.String()
}
