package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"

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

// mtrLossColorTag returns the tview color tag for a hop's loss level.
func mtrLossColorTag(l stats.Level) string {
	switch l {
	case stats.LevelCrit:
		return "[red::b]"
	case stats.LevelWarn:
		return "[orange]"
	default:
		return "[green]"
	}
}

// rttColorTag returns the tview color tag for an RTT level.
func rttColorTag(l stats.Level) string {
	switch l {
	case stats.LevelCrit:
		return "[red]"
	case stats.LevelWarn:
		return "[orange]"
	case stats.LevelOK:
		return "[green]"
	default:
		return "[white]"
	}
}

// mtrFixedW is the width taken by every MTR column but Host, separators
// included, in the compact or full layout.
func mtrFixedW(compact bool) int {
	if compact {
		// Hop + Loss% + Snt + Last + Avg + 6 separators
		return mtrHopColW + mtrLossColW + mtrSntColW + mtrLatColW*2 + 6
	}
	// Hop + Loss% + Snt + Recv + Last + Avg + Min + Max + Jitter + 10 separators
	return mtrHopColW + mtrLossColW + mtrSntColW + mtrRecvColW + mtrLatColW*5 + 10
}

// mtrHostColW computes the Host column width for the MTR table given the
// total available width and whether compact mode is active.
func mtrHostColW(availW int, compact bool) int {
	return max(availW-mtrFixedW(compact), minMTRHostW)
}

// renderMTRTable builds the MTR monitor pane string.
// One table per target, separated by a blank line.
// Columns: Hop, Host(ASN), Loss%, Snt, [Recv,] Last, Avg, [Min, Max, Jitter]
func renderMTRTable(targets []*stats.TargetStats, availW int, srcIPv4, srcIPv6 string) string {
	// Compact mode: drop Recv/Min/Max/Jitter columns when screen is narrow.
	// Threshold: minimum width to fit all columns with minMTRHostW host column.
	compact := availW < mtrFixedW(false)+minMTRHostW

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

	// Compact: Hop | Host | Loss% | Snt | Last | Avg
	// Full:    Hop | Host | Loss% | Snt | Recv | Last | Avg | Min | Max | Jitter
	rtt := func(get func(stats.HopView) time.Duration) func(stats.HopView) boxCell {
		return rttCell(get, formatRTT, view.Thresholds.RTT)
	}
	all := []monitorColumn[stats.HopView]{
		{boxColumn{header: "Hop", width: mtrHopColW}, plainCell(func(h stats.HopView) string { return fmt.Sprintf("%3d.", h.TTL) }), false},
		{boxColumn{header: "Host", width: hostW}, plainCell(mtrIPStr), false},
		{boxColumn{header: "Loss%", width: mtrLossColW}, func(h stats.HopView) boxCell {
			return boxCell{text: fmt.Sprintf("%.1f%%", h.LossPct), tag: mtrLossColorTag(view.Thresholds.HopLoss(h.LossPct))}
		}, false},
		{boxColumn{header: "Snt", width: mtrSntColW}, plainCell(func(h stats.HopView) string { return fmt.Sprintf("%d", h.Sent) }), false},
		{boxColumn{header: "Recv", width: mtrRecvColW}, plainCell(func(h stats.HopView) string { return fmt.Sprintf("%d", h.Recv) }), true},
		{boxColumn{header: "Last", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.LastRTT }), false},
		{boxColumn{header: "Avg", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.AvgRTT }), false},
		{boxColumn{header: "Min", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.MinRTT }), true},
		{boxColumn{header: "Max", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.MaxRTT }), true},
		{boxColumn{header: "Jitter", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.Jitter }), true},
	}
	cols, boxCols := layoutColumns(all, compact)

	// One row per hop, with no rule between hops.
	var rows [][]boxCell
	for _, h := range hops {
		rows = append(rows, rowCells(cols, h))
	}
	var groups [][][]boxCell
	if len(rows) > 0 {
		groups = [][][]boxCell{rows}
	}
	writeLabelledBoxTable(sb, boxCols, label, groups, " Discovering...")
}

// mtrIPStr returns the display string for a hop's IP, PTR name and AS.
func mtrIPStr(h stats.HopView) string {
	if h.IP == "" {
		return "*"
	}
	out := h.IP
	if h.PTR != "" {
		out += " " + h.PTR
	}
	if annotation := stats.FormatASN(h.ASN, h.Org); annotation != "" {
		out += " (" + annotation + ")"
	}
	return tview.Escape(out)
}
