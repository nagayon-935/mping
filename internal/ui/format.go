package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// fixedMsWidth fits "99999.99ms". Larger values are ellipsized in
// fixed-width table cells. Box columns also reserve two padding cells.
const (
	fixedMsWidth = len("99999.99ms")
	fixedMsColW  = fixedMsWidth + 2
)

// formatFixedMs formats a duration as milliseconds with 2 decimal places
// (e.g. "123.45ms"), or "-" when there is no value.
func formatFixedMs(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
}

func calcLossRate(view stats.TargetView) float64 {
	return stats.LossRatio(view.Recv, view.Loss)
}

// timeAgoColW fits elapsed times up to "99h59m59s ago" with box padding;
// anything longer is ellipsized rather than widening the column for every row.
const timeAgoColW = len(" 99h59m59s ago ")

func formatLossAgo(lastLossTime time.Time) string {
	if lastLossTime.IsZero() {
		return "-"
	}
	ago := time.Since(lastLossTime).Round(time.Second)
	return fmt.Sprintf("%s ago", ago)
}

// intOrDash formats n, or "-" when it is unknown (<= 0). Used for the TTL and
// MTU cells.
func intOrDash(n int) string {
	if n <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

// inferInitialTTL returns the likely initial TTL the remote host used,
// inferred from the received TTL (LastTTL).
// Common OS defaults: 64 (Linux/macOS), 128 (Windows), 255 (network devices).
func inferInitialTTL(lastTTL int) string {
	switch {
	case lastTTL <= 0:
		return "-"
	case lastTTL <= 64:
		return "64"
	case lastTTL <= 128:
		return "128"
	default:
		return "255"
	}
}

// hopLabels labels each hop as the route pane shows it: "IP(ASN Org) ptr",
// or "*" for a hop that never answered.
func hopLabels(hops []stats.HopView) []string {
	labels := make([]string, len(hops))
	for i, h := range hops {
		labels[i] = stats.FormatHop(h.IP, h.ASN, h.Org, h.PTR)
	}
	return labels
}

// wrapHops splits hops into lines that fit within maxWidth display columns.
// Each hop is joined with " -> " and a new line is started when adding the
// next hop would exceed maxWidth.
func wrapHops(hops []string, maxWidth int) []string {
	if len(hops) == 0 || maxWidth <= 0 {
		return nil
	}
	var lines []string
	var current []string
	currentW := 0
	for _, hop := range hops {
		var segment string
		if len(current) == 0 {
			segment = hop
		} else {
			segment = " -> " + hop
		}
		segW := runewidth.StringWidth(segment)
		if len(current) > 0 && currentW+segW > maxWidth {
			lines = append(lines, strings.Join(current, " -> "))
			current = []string{hop}
			currentW = runewidth.StringWidth(hop)
		} else {
			current = append(current, hop)
			currentW += segW
		}
	}
	if len(current) > 0 {
		lines = append(lines, strings.Join(current, " -> "))
	}
	return lines
}

func truncateToDisplayWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	limit := width - 3
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if rw == 0 {
			rw = 1
		}
		if cur+rw > limit {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String() + "..."
}

func formatCellText(text string, width int, align int) string {
	text = truncateToDisplayWidth(text, width)
	textWidth := runewidth.StringWidth(text)
	if textWidth >= width {
		return text
	}
	pad := strings.Repeat(" ", width-textWidth)
	if align == tview.AlignRight {
		return pad + text
	}
	return text + pad
}

// paddedCell left-aligns text between one space of padding on either side.
func paddedCell(text string, colW int) string {
	return padBoxCell(text, colW, tview.AlignLeft)
}

// rightPaddedCell right-aligns text between one space of padding on either side.
func rightPaddedCell(text string, colW int) string {
	return padBoxCell(text, colW, tview.AlignRight)
}

func padBoxCell(text string, colW, align int) string {
	return " " + formatCellText(text, colW-2, align) + " "
}
