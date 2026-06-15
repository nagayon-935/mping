package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

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
func renderHTTPMonitorTable(results []*stats.HTTPCheckResult, availW int, lastStatuses map[string]string, errorLogs *[]string, errorView interface {
	Write(p []byte) (n int, err error)
}) string {
	// Compact: drop Min/Avg/Max/Since when screen is narrow.
	// +2 accounts for the outer left/right border chars that consume available width.
	fullFixed := minHTTPURLW + httpStatusColW + httpCodeColW + httpLatColW*4 + httpCountColW*2 + httpSinceColW + 9 + 2
	compact := availW < fullFixed

	urlW := httpURLColW(results, availW, compact)

	// Detect status changes and log them.
	if lastStatuses != nil && errorLogs != nil {
		for _, r := range results {
			v := r.GetView()
			if v.Status == "" || v.Status == "Checking..." {
				continue
			}
			prev, seen := lastStatuses[v.URL]
			if seen && prev != v.Status {
				color := "[yellow]"
				if v.Status == "Up" {
					color = "[green]"
				}
				msg := fmt.Sprintf("[darkgray]%s[-] [white]HTTP %s:[white] %s → %s%s[-]",
					time.Now().Format("15:04:05"), v.URL, prev, color, v.Status)
				if el, ok := errorView.(interface {
					Write(p []byte) (n int, err error)
				}); ok && el != nil {
					appendErrorLogRaw(errorLogs, el, msg)
				}
			}
			lastStatuses[v.URL] = v.Status
		}
	}

	var sb strings.Builder

	if compact {
		// Compact: URL | Status | Code | Last | Up | Down
		cols := []int{urlW, httpStatusColW, httpCodeColW, httpLatColW, httpCountColW, httpCountColW}
		innerW := urlW + httpStatusColW + httpCodeColW + httpLatColW + httpCountColW*2 + 5

		fmt.Fprintln(&sb, boxBorder([]int{innerW}, borderTop))
		fmt.Fprintln(&sb, boxBorder(cols, borderIntro))
		fmt.Fprintln(&sb, boxHeaderRow([]string{"URL", "Status", "Code", "Last", "Up", "Down"}, cols))
		fmt.Fprintln(&sb, boxBorder(cols, borderMid))

		if len(results) == 0 {
			fmt.Fprintln(&sb, boxSpanRow(" Waiting for results...", innerW, "[darkgray]"))
		} else {
			for i, r := range results {
				v := r.GetView()
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[-]\n",
					paddedCell(v.URL, urlW),
					httpStatusColorTag(v.Status), paddedCell(v.Status, httpStatusColW),
					httpCodeColorTag(v.StatusCode), paddedCell(httpCodeStr(v.StatusCode), httpCodeColW),
					mtrRTTColorTag(v.RTT), paddedCell(formatHTTPRTT(v.RTT), httpLatColW),
					paddedCell(fmt.Sprintf("%d", v.UpCount), httpCountColW),
					paddedCell(fmt.Sprintf("%d", v.DownCount), httpCountColW))
				if i < len(results)-1 {
					fmt.Fprintln(&sb, boxBorder(cols, borderMid))
				}
			}
		}
		fmt.Fprintln(&sb, boxBorder(cols, borderBottom))
	} else {
		// Full: URL | Status | Code | Last | Min | Avg | Max | Up | Down | Since
		cols := []int{urlW, httpStatusColW, httpCodeColW, httpLatColW, httpLatColW, httpLatColW,
			httpLatColW, httpCountColW, httpCountColW, httpSinceColW}
		innerW := urlW + httpStatusColW + httpCodeColW + httpLatColW*4 + httpCountColW*2 + httpSinceColW + 9

		fmt.Fprintln(&sb, boxBorder([]int{innerW}, borderTop))
		fmt.Fprintln(&sb, boxBorder(cols, borderIntro))
		fmt.Fprintln(&sb, boxHeaderRow([]string{"URL", "Status", "Code", "Last", "Min", "Avg",
			"Max", "Up", "Down", "Since"}, cols))
		fmt.Fprintln(&sb, boxBorder(cols, borderMid))

		if len(results) == 0 {
			fmt.Fprintln(&sb, boxSpanRow(" Waiting for results...", innerW, "[darkgray]"))
		} else {
			for i, r := range results {
				v := r.GetView()
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					paddedCell(v.URL, urlW),
					httpStatusColorTag(v.Status), paddedCell(v.Status, httpStatusColW),
					httpCodeColorTag(v.StatusCode), paddedCell(httpCodeStr(v.StatusCode), httpCodeColW),
					mtrRTTColorTag(v.RTT), paddedCell(formatHTTPRTT(v.RTT), httpLatColW),
					mtrRTTColorTag(v.MinRTT), paddedCell(formatHTTPRTT(v.MinRTT), httpLatColW),
					mtrRTTColorTag(v.AvgRTT), paddedCell(formatHTTPRTT(v.AvgRTT), httpLatColW),
					mtrRTTColorTag(v.MaxRTT), paddedCell(formatHTTPRTT(v.MaxRTT), httpLatColW),
					paddedCell(fmt.Sprintf("%d", v.UpCount), httpCountColW),
					paddedCell(fmt.Sprintf("%d", v.DownCount), httpCountColW),
					paddedCell(httpSinceStr(v.LastChange), httpSinceColW))
				if i < len(results)-1 {
					fmt.Fprintln(&sb, boxBorder(cols, borderMid))
				}
			}
		}
		fmt.Fprintln(&sb, boxBorder(cols, borderBottom))
	}

	return sb.String()
}

// appendErrorLogRaw appends a pre-formatted message to the error log slice and view.
func appendErrorLogRaw(logs *[]string, view interface {
	Write(p []byte) (n int, err error)
}, msg string) {
	*logs = append(*logs, msg)
	if len(*logs) > errorLogMaxSize {
		*logs = (*logs)[len(*logs)-errorLogMaxSize:]
	}
	fmt.Fprintln(view, msg)
}
