package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

func formatRTT(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%v", d.Round(time.Microsecond))
}

func calcLossRate(view stats.TargetView) float64 {
	totalAttempts := view.Recv + view.Loss
	if totalAttempts == 0 {
		return 0.0
	}
	return (float64(view.Loss) / float64(totalAttempts)) * 100
}

func formatLossAgo(lastLossTime time.Time) string {
	if lastLossTime.IsZero() {
		return "-"
	}
	ago := time.Since(lastLossTime).Round(time.Second)
	return fmt.Sprintf("%s ago", ago)
}

func ttlString(ttl int) string {
	if ttl <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", ttl)
}

// dscpDisplayString formats the DSCP column cell for a target's most recent
// reply. Mirrors ttlString's "<=0 means unknown" convention: 0 is both the
// pre-first-reply default and IPv4's permanent value (x/net has no
// receive-side DSCP support — see pinger.Reply.DSCP), so it reads as "-"
// rather than the misleading "CS0".
func dscpDisplayString(dscp int) string {
	if dscp <= 0 {
		return "-"
	}
	return pinger.DSCPName(dscp)
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

func hopCountString(hops []string) string {
	if len(hops) == 0 {
		return "-"
	}
	// Status messages occupy the route field but are not measured hops.
	if len(hops) == 1 && (hops[0] == "Tracing..." || hops[0] == "no route found" || strings.HasPrefix(hops[0], "error: ")) {
		return "-"
	}
	return fmt.Sprintf("%d", len(hops))
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

func mtuString(mtu int) string {
	if mtu <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", mtu)
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
	if width <= 0 {
		return ""
	}
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

// paddedCell returns text with a leading space, right-padded to fill colW.
func paddedCell(text string, colW int) string {
	return formatCellText(" "+text, colW, tview.AlignLeft)
}

// rightPaddedCell returns text with a trailing space, left-padded to fill colW.
func rightPaddedCell(text string, colW int) string {
	if text == "" {
		return formatCellText("", colW, tview.AlignRight)
	}
	return formatCellText(text+" ", colW, tview.AlignRight)
}
