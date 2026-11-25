package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	mtrHopColW  = 5  // " Hop " — "   1." fits exactly
	mtrLossColW = 10 // " × 100.0% "
	mtrSntColW  = 6  // " Snt  "
	mtrRecvColW = 6  // " Recv "
	mtrLatColW  = 9  // " 12.1ms  "
	minMTRHostW = 16
)

type mtrRow struct {
	stats.HopView
	lossLevel     stats.Level
	nonPersistent bool
}

// mtrLossCell displays raw loss with the effective level's styling.
func mtrLossCell(h mtrRow) boxCell {
	text := fmt.Sprintf("%.1f%%", h.LossPct)
	switch h.lossLevel {
	case stats.LevelWarn, stats.LevelCrit:
		return boxCell{text: statusSymbol(h.lossLevel) + " " + text, tag: targetStatusTags.of(h.lossLevel)}
	default:
		if h.nonPersistent {
			return boxCell{text: text, tag: "[gray]"}
		}
		return boxCell{text: text}
	}
}

// rttColorTag returns the tview color tag for an RTT level.
func rttColorTag(l stats.Level) string {
	return levelTags{crit: "[red]", warn: "[orange]", ok: "[green]", other: "[white]"}.of(l)
}

// renderMTRTable builds the MTR monitor pane string.
// One table per target, separated by a blank line.
// Columns: Hop, Host(ASN), Loss%, Snt, [Recv,] Last, Avg, [Min, Max, Jitter]
func renderMTRTable(targets []*stats.TargetStats, availW int, sources sourceIPs) string {
	var sb strings.Builder
	for ti, t := range targets {
		renderMTRTargetTable(&sb, t, availW, sources)
		if ti < len(targets)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderMTRTargetTable renders one target's hop table.
func renderMTRTargetTable(sb *strings.Builder, t *stats.TargetStats, availW int, sources sourceIPs) {
	view := t.GetViewWindow(0)
	hops := view.MTRHops
	effective := stats.EffectiveHopLosses(hops)
	hopRows := make([]mtrRow, len(hops))
	level := targetStatusLevel(view)
	summary := statusSymbol(level) + " " + statusLabel(level)
	firstLoss := true
	for i, h := range hops {
		lossLevel := view.Thresholds.HopLoss(effective[i])
		hopRows[i] = mtrRow{HopView: h, lossLevel: lossLevel, nonPersistent: h.LossPct > effective[i]}
		if firstLoss && lossLevel != stats.LevelOK {
			summary += fmt.Sprintf(" · loss from hop %d", h.TTL)
			firstLoss = false
		}
	}

	// Target label: "SrcIP -> DstIP" (with hostname prefix when applicable)
	srcIP := sources.forDst(view.IP)
	dstIP := view.IP
	if dstIP == "" {
		dstIP = view.Host
	}
	label := fmt.Sprintf("%s -> %s", srcIP, dstIP)
	if view.Host != "" && view.Host != view.IP {
		label = fmt.Sprintf("%s (%s -> %s)", view.Host, srcIP, dstIP)
	}
	if view.MTRFlapCount > 0 {
		label += fmt.Sprintf("  [FLAP %d times %s]", view.MTRFlapCount, view.MTRLastFlapAt.Format(logTimeLayout))
	}

	// Compact: Hop | Host | Loss% | Snt | Last | Avg
	// Full:    Hop | Host | Loss% | Snt | Recv | Last | Avg | Min | Max | Jitter
	rtt := func(get func(stats.HopView) time.Duration) func(mtrRow) boxCell {
		cell := rttCell(get, formatRTT, view.Thresholds.RTT)
		return func(h mtrRow) boxCell { return cell(h.HopView) }
	}
	all := []monitorColumn[mtrRow]{
		{boxColumn{header: "Hop", width: mtrHopColW}, plainCell(func(h mtrRow) string { return fmt.Sprintf("%3d.", h.TTL) }), false},
		{boxColumn{header: "Host", width: minMTRHostW}, func(h mtrRow) boxCell {
			cell := boxCell{text: mtrIPStr(h.HopView)}
			if h.IP == "" && h.lossLevel == stats.LevelOK {
				cell.tag = "[gray]"
			}
			return cell
		}, false},
		{boxColumn{header: "Loss%", width: mtrLossColW}, mtrLossCell, false},
		{boxColumn{header: "Snt", width: mtrSntColW}, plainCell(func(h mtrRow) string { return fmt.Sprintf("%d", h.Sent) }), false},
		{boxColumn{header: "Recv", width: mtrRecvColW}, plainCell(func(h mtrRow) string { return fmt.Sprintf("%d", h.Recv) }), true},
		{boxColumn{header: "Last", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.LastRTT }), false},
		{boxColumn{header: "Avg", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.AvgRTT }), false},
		{boxColumn{header: "Min", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.MinRTT }), true},
		{boxColumn{header: "Max", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.MaxRTT }), true},
		{boxColumn{header: "Jitter", width: mtrLatColW}, rtt(func(h stats.HopView) time.Duration { return h.Jitter }), true},
	}
	cols, boxCols := fitMonitorColumns(all, "Host", availW)

	// One row per hop, with no rule between hops.
	var groups [][][]boxCell
	if len(hopRows) > 0 {
		rows := make([][]boxCell, len(hopRows))
		for i, h := range hopRows {
			rows[i] = rowCells(cols, h)
		}
		groups = [][][]boxCell{rows}
	}
	writeLabelledBoxTable(sb, boxCols, summary+" — "+label, groups, " Discovering...")
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
