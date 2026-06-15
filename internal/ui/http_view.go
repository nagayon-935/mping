package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	httpStatusColW = 12 // " Checking... "
	httpCodeColW   = 6  // "  200 "
	httpCountColW  = 7  // " 99999 "
	httpLatColW    = 9  // same as mtrLatColW — " 1.234ms "
	httpSinceColW  = 12 // " 1h23m45s  "
	minHTTPURLW    = 20
)

// httpURLColW returns the URL column width given available terminal width and
// whether compact mode is active.
func httpURLColW(availW int, compact bool) int {
	var fixedW int
	if compact {
		// URL + Status + Code + Last + Up + Down + 5 separators
		fixedW = httpStatusColW + httpCodeColW + httpLatColW + httpCountColW*2 + 5
	} else {
		// URL + Status + Code + Last + Min + Avg + Max + Up + Down + Since + 9 separators
		fixedW = httpStatusColW + httpCodeColW + httpLatColW*4 + httpCountColW*2 + httpSinceColW + 9
	}
	w := availW - fixedW
	if w < minHTTPURLW {
		w = minHTTPURLW
	}
	return w
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
	fullFixed := minHTTPURLW + httpStatusColW + httpCodeColW + httpLatColW*4 + httpCountColW*2 + httpSinceColW + 9
	compact := availW < fullFixed

	urlW := httpURLColW(availW, compact)

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
		innerW := urlW + httpStatusColW + httpCodeColW + httpLatColW + httpCountColW*2 + 5
		top := strings.Repeat("─", innerW)
		hu := strings.Repeat("─", urlW)
		hs := strings.Repeat("─", httpStatusColW)
		hc := strings.Repeat("─", httpCodeColW)
		hl := strings.Repeat("─", httpLatColW)
		hct := strings.Repeat("─", httpCountColW)

		fmt.Fprintf(&sb, "[white]┌%s┐[-]\n", top)
		fmt.Fprintf(&sb, "[white]├%s┬%s┬%s┬%s┬%s┬%s┤[-]\n", hu, hs, hc, hl, hct, hct)
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			paddedCell("URL", urlW),
			paddedCell("Status", httpStatusColW),
			paddedCell("Code", httpCodeColW),
			paddedCell("Last", httpLatColW),
			paddedCell("Up", httpCountColW),
			paddedCell("Down", httpCountColW))
		fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┤[-]\n", hu, hs, hc, hl, hct, hct)

		if len(results) == 0 {
			fmt.Fprintf(&sb, "[white]│[darkgray]%s[white]│[-]\n",
				formatCellText(" Waiting for results...", innerW, tview.AlignLeft))
		} else {
			for _, r := range results {
				v := r.GetView()
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[-]\n",
					paddedCell(v.URL, urlW),
					httpStatusColorTag(v.Status), paddedCell(v.Status, httpStatusColW),
					httpCodeColorTag(v.StatusCode), paddedCell(httpCodeStr(v.StatusCode), httpCodeColW),
					mtrRTTColorTag(v.RTT), paddedCell(formatRTT(v.RTT), httpLatColW),
					paddedCell(fmt.Sprintf("%d", v.UpCount), httpCountColW),
					paddedCell(fmt.Sprintf("%d", v.DownCount), httpCountColW))
			}
		}
		fmt.Fprintf(&sb, "[white]└%s┴%s┴%s┴%s┴%s┴%s┘[-]\n", hu, hs, hc, hl, hct, hct)
	} else {
		// Full: URL | Status | Code | Last | Min | Avg | Max | Up | Down | Since
		innerW := urlW + httpStatusColW + httpCodeColW + httpLatColW*4 + httpCountColW*2 + httpSinceColW + 9
		top := strings.Repeat("─", innerW)
		hu := strings.Repeat("─", urlW)
		hs := strings.Repeat("─", httpStatusColW)
		hc := strings.Repeat("─", httpCodeColW)
		hl := strings.Repeat("─", httpLatColW)
		hct := strings.Repeat("─", httpCountColW)
		hsi := strings.Repeat("─", httpSinceColW)

		fmt.Fprintf(&sb, "[white]┌%s┐[-]\n", top)
		fmt.Fprintf(&sb, "[white]├%s┬%s┬%s┬%s┬%s┬%s┬%s┬%s┬%s┬%s┤[-]\n",
			hu, hs, hc, hl, hl, hl, hl, hct, hct, hsi)
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			paddedCell("URL", urlW),
			paddedCell("Status", httpStatusColW),
			paddedCell("Code", httpCodeColW),
			paddedCell("Last", httpLatColW),
			paddedCell("Min", httpLatColW),
			paddedCell("Avg", httpLatColW),
			paddedCell("Max", httpLatColW),
			paddedCell("Up", httpCountColW),
			paddedCell("Down", httpCountColW),
			paddedCell("Since", httpSinceColW))
		fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┼%s┼%s┼%s┼%s┤[-]\n",
			hu, hs, hc, hl, hl, hl, hl, hct, hct, hsi)

		if len(results) == 0 {
			fmt.Fprintf(&sb, "[white]│[darkgray]%s[white]│[-]\n",
				formatCellText(" Waiting for results...", innerW, tview.AlignLeft))
		} else {
			for _, r := range results {
				v := r.GetView()
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					paddedCell(v.URL, urlW),
					httpStatusColorTag(v.Status), paddedCell(v.Status, httpStatusColW),
					httpCodeColorTag(v.StatusCode), paddedCell(httpCodeStr(v.StatusCode), httpCodeColW),
					mtrRTTColorTag(v.RTT), paddedCell(formatRTT(v.RTT), httpLatColW),
					mtrRTTColorTag(v.MinRTT), paddedCell(formatRTT(v.MinRTT), httpLatColW),
					mtrRTTColorTag(v.AvgRTT), paddedCell(formatRTT(v.AvgRTT), httpLatColW),
					mtrRTTColorTag(v.MaxRTT), paddedCell(formatRTT(v.MaxRTT), httpLatColW),
					paddedCell(fmt.Sprintf("%d", v.UpCount), httpCountColW),
					paddedCell(fmt.Sprintf("%d", v.DownCount), httpCountColW),
					paddedCell(httpSinceStr(v.LastChange), httpSinceColW))
			}
		}
		fmt.Fprintf(&sb, "[white]└%s┴%s┴%s┴%s┴%s┴%s┴%s┴%s┴%s┴%s┘[-]\n",
			hu, hs, hc, hl, hl, hl, hl, hct, hct, hsi)
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
