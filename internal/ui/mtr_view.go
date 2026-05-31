package ui

import (
	"fmt"
	"strings"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	mtrHopColW    = 4  // " Hop"
	mtrSntColW    = 5  // " Snt"
	mtrRecvColW   = 5  // " Recv"
	mtrLossColW   = 7  // " Loss%"
	mtrLatColW    = 8  // " Last ms" etc.
	minMTRHostW   = 18
)

// renderMTRTable builds the MTR monitor pane string.
// Format per target block (mtr-standard columns):
//
//	Hop  Host(ASN)       Loss%  Snt  Recv  Last   Avg   Min   Max  Jitter
func renderMTRTable(targets []*stats.TargetStats, availW int) string {
	var sb strings.Builder

	for ti, t := range targets {
		view := t.GetView()
		hops := view.MTRHops
		if len(hops) == 0 {
			hops = nil // show discovering header
		}

		// Per-target header: "── host (ip) ──"
		hostLabel := view.Host
		if view.IP != "" && view.IP != view.Host {
			hostLabel = fmt.Sprintf("%s (%s)", view.Host, view.IP)
		}
		renderMTRTargetBlock(&sb, hostLabel, hops, availW)

		if ti < len(targets)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderMTRTargetBlock renders one target's hop table into sb.
func renderMTRTargetBlock(sb *strings.Builder, hostLabel string, hops []stats.HopView, availW int) {
	// Available width for the host column after fixed columns
	// Fixed: Hop(4) + Loss%(7) + Snt(5) + Recv(5) + Last(8) + Avg(8) + Min(8) + Max(8) + Jitter(8) + separators
	fixedW := mtrHopColW + mtrLossColW + mtrSntColW + mtrRecvColW + mtrLatColW*5 + 11
	hostColW := availW - fixedW
	if hostColW < minMTRHostW {
		hostColW = minMTRHostW
	}

	// Compact mode: drop Min/Max/Jitter when screen is narrow
	compact := (availW - mtrHopColW - mtrLossColW - mtrSntColW - mtrRecvColW - mtrLatColW*2 - hostColW - 8) < 0

	// ── Target header ──
	headerLine := truncateOrPad(hostLabel, availW)
	fmt.Fprintf(sb, "[yellow::b]%s[-]\n", headerLine)

	// ── Column header ──
	if compact {
		fmt.Fprintf(sb, "[white::b]%s%s%s%s%s%s[-]\n",
			rightPaddedCell("Hop", mtrHopColW),
			rightPaddedCell("Host", hostColW),
			paddedCell("Loss%", mtrLossColW),
			paddedCell("Snt", mtrSntColW),
			paddedCell("Last", mtrLatColW),
			paddedCell("Avg", mtrLatColW),
		)
	} else {
		fmt.Fprintf(sb, "[white::b]%s%s%s%s%s%s%s%s%s%s[-]\n",
			rightPaddedCell("Hop", mtrHopColW),
			rightPaddedCell("Host", hostColW),
			paddedCell("Loss%", mtrLossColW),
			paddedCell("Snt", mtrSntColW),
			paddedCell("Recv", mtrRecvColW),
			paddedCell("Last", mtrLatColW),
			paddedCell("Avg", mtrLatColW),
			paddedCell("Min", mtrLatColW),
			paddedCell("Max", mtrLatColW),
			paddedCell("Jitter", mtrLatColW),
		)
	}

	if len(hops) == 0 {
		fmt.Fprintf(sb, "[white] Discovering...[-]\n")
		return
	}

	for _, h := range hops {
		hopStr := fmt.Sprintf("%3d.", h.TTL)
		ipStr := h.IP
		if ipStr == "" {
			ipStr = "*"
		} else if h.ASN != "" {
			ipStr = fmt.Sprintf("%s (%s)", ipStr, h.ASN)
		}

		lossColor := lossColorForRate(h.LossPct)
		lossStr := fmt.Sprintf("%.1f%%", h.LossPct)

		if compact {
			fmt.Fprintf(sb, "[white]%s[-][white]%s[-][%s]%s[-][white]%s[-][white]%s[-][white]%s[-]\n",
				rightPaddedCell(hopStr, mtrHopColW),
				rightPaddedCell(ipStr, hostColW),
				lossColor.String(), paddedCell(lossStr, mtrLossColW),
				paddedCell(fmt.Sprintf("%d", h.Sent), mtrSntColW),
				paddedCell(formatRTT(h.LastRTT), mtrLatColW),
				paddedCell(formatRTT(h.AvgRTT), mtrLatColW),
			)
		} else {
			fmt.Fprintf(sb, "[white]%s[-][white]%s[-][%s]%s[-][white]%s[-][white]%s[-][white]%s[-][white]%s[-][white]%s[-][white]%s[-][white]%s[-]\n",
				rightPaddedCell(hopStr, mtrHopColW),
				rightPaddedCell(ipStr, hostColW),
				lossColor.String(), paddedCell(lossStr, mtrLossColW),
				paddedCell(fmt.Sprintf("%d", h.Sent), mtrSntColW),
				paddedCell(fmt.Sprintf("%d", h.Recv), mtrRecvColW),
				paddedCell(formatRTT(h.LastRTT), mtrLatColW),
				paddedCell(formatRTT(h.AvgRTT), mtrLatColW),
				paddedCell(formatRTT(h.MinRTT), mtrLatColW),
				paddedCell(formatRTT(h.MaxRTT), mtrLatColW),
				paddedCell(formatRTT(h.Jitter), mtrLatColW),
			)
		}
	}
}

// truncateOrPad truncates or right-pads s to exactly width runes.
func truncateOrPad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
}
