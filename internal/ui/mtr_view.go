package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	mtrHopColW  = 5 // " Hop " — "  1. " fits exactly
	mtrLossColW = 8 // " 100.0% "
	mtrSntColW  = 6 // "  Snt  "
	mtrRecvColW = 6 // " Recv  "
	mtrLatColW  = 9 // " 12.1ms  "
	minMTRHostW = 16
)

// mtrLossColorTag returns a tview color tag string based on loss percentage.
func mtrLossColorTag(pct float64) string {
	th := getActiveThresholds()
	if pct >= th.LossCrit {
		return "[red::b]"
	}
	if pct > 0 {
		return "[orange]"
	}
	return "[green]"
}

// mtrRTTColorTag returns a tview color tag string based on RTT.
func mtrRTTColorTag(rtt time.Duration) string {
	if rtt == 0 {
		return "[white]"
	}
	th := getActiveThresholds()
	if rtt > th.RTTCrit {
		return "[red]"
	}
	if rtt > th.RTTWarn {
		return "[orange]"
	}
	return "[green]"
}

// mtrHostColW computes the Host column width for the MTR table given the
// total available width and whether compact mode is active.
func mtrHostColW(availW int, compact bool) int {
	var fixedW int
	if compact {
		// Hop + Loss% + Snt + Last + Avg + 6 separators
		fixedW = mtrHopColW + mtrLossColW + mtrSntColW + mtrLatColW*2 + 6
	} else {
		// Hop + Loss% + Snt + Recv + Last + Avg + Min + Max + Jitter + 10 separators
		fixedW = mtrHopColW + mtrLossColW + mtrSntColW + mtrRecvColW + mtrLatColW*5 + 10
	}
	w := availW - fixedW
	if w < minMTRHostW {
		w = minMTRHostW
	}
	return w
}

// renderMTRTable builds the MTR monitor pane string.
// One table per target, separated by a blank line.
// Columns: Hop, Host(ASN), Loss%, Snt, [Recv,] Last, Avg, [Min, Max, Jitter]
func renderMTRTable(targets []*stats.TargetStats, availW int, srcIPv4, srcIPv6 string) string {
	// Compact mode: drop Recv/Min/Max/Jitter columns when screen is narrow.
	// Threshold: minimum width to fit all columns with minMTRHostW host column.
	fullFixed := mtrHopColW + mtrLossColW + mtrSntColW + mtrRecvColW + mtrLatColW*5 + minMTRHostW + 10
	compact := availW < fullFixed

	hostW := mtrHostColW(availW, compact)

	var sb strings.Builder
	for ti, t := range targets {
		renderMTRTargetTable(&sb, t, hostW, compact, srcIPv4, srcIPv6)
		if ti < len(targets)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderMTRTargetTable renders one target's hop table.
func renderMTRTargetTable(sb *strings.Builder, t *stats.TargetStats, hostW int, compact bool, srcIPv4, srcIPv6 string) {
	view := t.GetView()
	hops := view.MTRHops

	// Target label: "SrcIP -> DstIP" (with hostname prefix when applicable)
	srcIP := displaySourceIPForDst(view.IP, srcIPv4, srcIPv6)
	dstIP := view.IP
	if dstIP == "" {
		dstIP = view.Host
	}
	label := fmt.Sprintf("%s -> %s", srcIP, dstIP)
	if view.Host != "" && view.Host != view.IP {
		label = fmt.Sprintf("%s (%s -> %s)", view.Host, srcIP, dstIP)
	}
	if view.MTRFlapCount > 0 {
		label += fmt.Sprintf("  [FLAP ×%d %s]", view.MTRFlapCount, view.MTRLastFlapAt.Format("15:04:05"))
	}

	if compact {
		// ── Compact: Hop | Host | Loss% | Snt | Last | Avg ──────────────
		cols := []int{mtrHopColW, hostW, mtrLossColW, mtrSntColW, mtrLatColW, mtrLatColW}
		// innerW = sum of all column widths + internal separators (5 for 6 cols)
		innerW := mtrHopColW + hostW + mtrLossColW + mtrSntColW + mtrLatColW*2 + 5

		// Full-width top border → label row → ├─┬─┤ introduces columns
		fmt.Fprintln(sb, boxBorder([]int{innerW}, borderTop))
		fmt.Fprintln(sb, boxSpanRow(" "+label, innerW, "[yellow::b]"))
		fmt.Fprintln(sb, boxBorder(cols, borderIntro))
		fmt.Fprintln(sb, boxHeaderRow([]string{"Hop", "Host", "Loss%", "Snt", "Last", "Avg"}, cols))
		fmt.Fprintln(sb, boxBorder(cols, borderMid))

		if len(hops) == 0 {
			fmt.Fprintln(sb, boxSpanRow(" Discovering...", innerW, "[darkgray]"))
		} else {
			for _, h := range hops {
				hopStr := fmt.Sprintf("%3d.", h.TTL)
				ipStr := mtrIPStr(h)
				lossStr := fmt.Sprintf("%.1f%%", h.LossPct)
				fmt.Fprintf(sb, "[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│%s%s[-][white]│%s%s[-][white]│[-]\n",
					paddedCell(hopStr, mtrHopColW),
					paddedCell(ipStr, hostW),
					mtrLossColorTag(h.LossPct), paddedCell(lossStr, mtrLossColW),
					paddedCell(fmt.Sprintf("%d", h.Sent), mtrSntColW),
					mtrRTTColorTag(h.LastRTT), paddedCell(formatRTT(h.LastRTT), mtrLatColW),
					mtrRTTColorTag(h.AvgRTT), paddedCell(formatRTT(h.AvgRTT), mtrLatColW))
			}
		}
		fmt.Fprintln(sb, boxBorder(cols, borderBottom))

	} else {
		// ── Full: Hop | Host | Loss% | Snt | Recv | Last | Avg | Min | Max | Jitter ──
		cols := []int{mtrHopColW, hostW, mtrLossColW, mtrSntColW, mtrRecvColW,
			mtrLatColW, mtrLatColW, mtrLatColW, mtrLatColW, mtrLatColW}
		// innerW = sum of all column widths + internal separators (9 for 10 cols)
		innerW := mtrHopColW + hostW + mtrLossColW + mtrSntColW + mtrRecvColW + mtrLatColW*5 + 9

		// Full-width top border → label row → ├─┬─┤ introduces columns
		fmt.Fprintln(sb, boxBorder([]int{innerW}, borderTop))
		fmt.Fprintln(sb, boxSpanRow(" "+label, innerW, "[yellow::b]"))
		fmt.Fprintln(sb, boxBorder(cols, borderIntro))
		fmt.Fprintln(sb, boxHeaderRow([]string{"Hop", "Host", "Loss%", "Snt", "Recv",
			"Last", "Avg", "Min", "Max", "Jitter"}, cols))
		fmt.Fprintln(sb, boxBorder(cols, borderMid))

		if len(hops) == 0 {
			fmt.Fprintln(sb, boxSpanRow(" Discovering...", innerW, "[darkgray]"))
		} else {
			for _, h := range hops {
				hopStr := fmt.Sprintf("%3d.", h.TTL)
				ipStr := mtrIPStr(h)
				lossStr := fmt.Sprintf("%.1f%%", h.LossPct)
				fmt.Fprintf(sb, "[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│%s%s[-][white]│[-]\n",
					paddedCell(hopStr, mtrHopColW),
					paddedCell(ipStr, hostW),
					mtrLossColorTag(h.LossPct), paddedCell(lossStr, mtrLossColW),
					paddedCell(fmt.Sprintf("%d", h.Sent), mtrSntColW),
					paddedCell(fmt.Sprintf("%d", h.Recv), mtrRecvColW),
					mtrRTTColorTag(h.LastRTT), paddedCell(formatRTT(h.LastRTT), mtrLatColW),
					mtrRTTColorTag(h.AvgRTT), paddedCell(formatRTT(h.AvgRTT), mtrLatColW),
					mtrRTTColorTag(h.MinRTT), paddedCell(formatRTT(h.MinRTT), mtrLatColW),
					mtrRTTColorTag(h.MaxRTT), paddedCell(formatRTT(h.MaxRTT), mtrLatColW),
					mtrRTTColorTag(h.Jitter), paddedCell(formatRTT(h.Jitter), mtrLatColW))
			}
		}
		fmt.Fprintln(sb, boxBorder(cols, borderBottom))
	}
}

// mtrIPStr returns the display string for a hop's IP/ASN/country.
func mtrIPStr(h stats.HopView) string {
	if h.IP == "" {
		return "*"
	}
	if h.ASN != "" {
		annotation := h.ASN
		if h.Country != "" {
			annotation += " " + h.Country
		}
		return fmt.Sprintf("%s (%s)", h.IP, annotation)
	}
	return h.IP
}
