package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	mtrHopColW  = 6  // " 255. "
	mtrLossColW = 10 // " × 100.0% "
	mtrSntColW  = 8  // " 999999 "
	mtrRecvColW = 8  // " 999999 "
	minMTRHostW = 16
)

type mtrRow struct {
	stats.HopView
	host          string
	lossLevel     stats.Level
	nonPersistent bool
}

// mtrLossCell displays raw loss with the effective level's styling.
func mtrLossCell(h mtrRow) boxCell {
	text := fmt.Sprintf("%.1f%%", h.LossPct)
	switch h.lossLevel {
	case stats.LevelWarn, stats.LevelCrit:
		return boxCell{text: statusSymbol(h.lossLevel) + " " + text, tag: levelTag(h.lossLevel)}
	default:
		if h.nonPersistent {
			return boxCell{text: text, tag: levelTag(stats.LevelNone)}
		}
		return boxCell{text: text}
	}
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
	hostWidth := minMTRHostW
	for i, h := range hops {
		lossLevel := view.Thresholds.HopLoss(effective[i])
		host := mtrIPStr(h)
		hostWidth = max(hostWidth, runewidth.StringWidth(host)+2)
		hopRows[i] = mtrRow{HopView: h, host: host, lossLevel: lossLevel, nonPersistent: h.LossPct > effective[i]}
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
		cell := rttCell(get, view.Thresholds.RTT)
		return func(h mtrRow) boxCell { return cell(h.HopView) }
	}
	all := []monitorColumn[mtrRow]{
		{boxColumn{header: "Hop", width: mtrHopColW, rightAlign: true}, plainCell(func(h mtrRow) string { return fmt.Sprintf("%d.", h.TTL) }), false},
		{boxColumn{header: "Host", width: minMTRHostW}, func(h mtrRow) boxCell {
			cell := boxCell{text: h.host}
			if h.IP == "" && h.lossLevel == stats.LevelOK {
				cell.tag = levelTag(stats.LevelNone)
			}
			return cell
		}, false},
		{boxColumn{header: "Loss%", width: mtrLossColW, rightAlign: true}, mtrLossCell, false},
		{boxColumn{header: "Snt", width: mtrSntColW, rightAlign: true}, plainCell(func(h mtrRow) string { return fmt.Sprintf("%d", h.Sent) }), false},
		{boxColumn{header: "Recv", width: mtrRecvColW, rightAlign: true}, plainCell(func(h mtrRow) string { return fmt.Sprintf("%d", h.Recv) }), true},
		{boxColumn{header: "Last", width: fixedMsColW, rightAlign: true}, rtt(func(h stats.HopView) time.Duration { return h.LastRTT }), false},
		{boxColumn{header: "Avg", width: fixedMsColW, rightAlign: true}, rtt(func(h stats.HopView) time.Duration { return h.AvgRTT }), false},
		{boxColumn{header: "Min", width: fixedMsColW, rightAlign: true}, rtt(func(h stats.HopView) time.Duration { return h.MinRTT }), true},
		{boxColumn{header: "Max", width: fixedMsColW, rightAlign: true}, rtt(func(h stats.HopView) time.Duration { return h.MaxRTT }), true},
		{boxColumn{header: "Jitter", width: fixedMsColW, rightAlign: true}, rtt(func(h stats.HopView) time.Duration { return h.Jitter }), true},
	}
	cols, boxCols := fitMonitorColumns(all, "Host", hostWidth, availW)
	if len(cols) == 0 {
		fmt.Fprintln(sb, truncateToDisplayWidth(monitorTooNarrow, availW))
		return
	}

	// One row per hop, with no rule between hops.
	var groups [][][]boxCell
	if len(hopRows) > 0 {
		rows := make([][]boxCell, len(hopRows))
		for i, h := range hopRows {
			rows[i] = rowCells(cols, h)
		}
		groups = [][][]boxCell{rows}
	}
	writeLabelledBoxTable(sb, boxCols, label, groups, "Discovering...")
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
	return out
}
