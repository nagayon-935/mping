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

	// Compact: Hop | Host | Loss% | Snt | Last | Avg
	// Full:    Hop | Host | Loss% | Snt | Recv | Last | Avg | Min | Max | Jitter
	type mtrColumn struct {
		boxColumn
		cell func(stats.HopView) boxCell
		full bool // shown only in the full layout
	}
	rtt := func(get func(stats.HopView) time.Duration) func(stats.HopView) boxCell {
		return func(h stats.HopView) boxCell { return boxCell{text: formatRTT(get(h)), tag: mtrRTTColorTag(get(h))} }
	}
	plain := func(text func(stats.HopView) string) func(stats.HopView) boxCell {
		return func(h stats.HopView) boxCell { return boxCell{text: text(h)} }
	}
	all := []mtrColumn{
		{boxColumn{header: "Hop", width: mtrHopColW}, plain(func(h stats.HopView) string { return fmt.Sprintf("%3d.", h.TTL) }), false},
		{boxColumn{header: "Host", width: hostW}, plain(mtrIPStr), false},
		{boxColumn{header: "Loss%", width: mtrLossColW}, func(h stats.HopView) boxCell {
			return boxCell{text: fmt.Sprintf("%.1f%%", h.LossPct), tag: mtrLossColorTag(h.LossPct)}
		}, false},
		{boxColumn{header: "Snt", width: mtrSntColW}, plain(func(h stats.HopView) string { return fmt.Sprintf("%d", h.Sent) }), false},
		{boxColumn{header: "Recv", width: mtrRecvColW}, plain(func(h stats.HopView) string { return fmt.Sprintf("%d", h.Recv) }), true},
		{boxColumn{header: "Last", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.LastRTT }), false},
		{boxColumn{header: "Avg", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.AvgRTT }), false},
		{boxColumn{header: "Min", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.MinRTT }), true},
		{boxColumn{header: "Max", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.MaxRTT }), true},
		{boxColumn{header: "Jitter", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.Jitter }), true},
	}
	var cols []mtrColumn
	for _, c := range all {
		if !c.full || !compact {
			cols = append(cols, c)
		}
	}
	boxCols := make([]boxColumn, len(cols))
	for i, c := range cols {
		boxCols[i] = c.boxColumn
	}

	// One row per hop, with no rule between hops.
	var rows [][]boxCell
	for _, h := range hops {
		cells := make([]boxCell, len(cols))
		for i, c := range cols {
			cells[i] = c.cell(h)
		}
		rows = append(rows, cells)
	}
	var groups [][][]boxCell
	if len(rows) > 0 {
		groups = [][][]boxCell{rows}
	}
	writeLabelledBoxTable(sb, boxCols, label, groups, " Discovering...")
}

// mtrIPStr returns the display string for a hop's IP/ASN/operator name.
func mtrIPStr(h stats.HopView) string {
	if h.IP == "" {
		return "*"
	}
	if annotation := stats.FormatASN(h.ASN, h.Org); annotation != "" {
		return fmt.Sprintf("%s (%s)", h.IP, annotation)
	}
	return h.IP
}
