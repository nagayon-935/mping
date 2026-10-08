package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

var tableErrorCandidates = []string{
	"DNS Error",
	"No Conn",
	"No IPv4 Conn",
	"No IPv6 Conn",
	"Timeout",
	"ICMP Error",
	"Destination Network Unreachable",
	"Destination Host Unreachable",
	"Destination Protocol Unreachable",
	"Destination Port Unreachable",
	"Fragmentation Needed",
	"Source Route Failed",
	"Destination Network Unknown",
	"Destination Host Unknown",
	"Source Host Isolated",
	"Network Administratively Prohibited",
	"Host Administratively Prohibited",
	"Network Unreachable for ToS",
	"Host Unreachable for ToS",
	"Communication Administratively Prohibited",
	"Host Precedence Violation",
	"Precedence Cutoff in Effect",
	"Destination Unreachable",
	"Time Exceeded",
	"Fragment Reassembly Time Exceeded",
	"Parameter Problem",
	"Missing Required Option",
	"Bad Length",
}

func formatTableError(err string) string {
	if err == "" {
		return ""
	}
	parts := strings.Split(err, ": ")
	return parts[len(parts)-1]
}

func calcInitialTableErrorWidth(targets []*stats.TargetStats, header string, minWidth int) int {
	maxWidth := runewidth.StringWidth(header)
	if minWidth > maxWidth {
		maxWidth = minWidth
	}
	for _, s := range tableErrorCandidates {
		if w := runewidth.StringWidth(s); w > maxWidth {
			maxWidth = w
		}
	}
	for _, t := range targets {
		if w := runewidth.StringWidth(formatTableError(t.GetView().LastError)); w > maxWidth {
			maxWidth = w
		}
	}
	return maxWidth
}

type compactRow struct {
	hostL string
	pathL string
	statL string
	errL  string
	hostR string
	pathR string
	statR string
	errR  string
}

type compactLayout struct {
	rows    []compactRow
	desired []int
	headers []string
	aligns  []int
	min     []int
	max     []int
}

func buildCompactLayout(views []stats.TargetView, packetSize int, sourceIPv4, sourceIPv6 string, errorMaxWidth int) compactLayout {
	headers := []string{"Host", "Path", "Stats", "Error"}
	aligns := []int{tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignLeft}
	desired := []int{
		runewidth.StringWidth(headers[0]),
		runewidth.StringWidth(headers[1]),
		runewidth.StringWidth(headers[2]),
		runewidth.StringWidth(headers[3]),
	}
	min := []int{8, 16, 18, 8}
	max := []int{40, 80, 80, errorMaxWidth}

	rows := make([]compactRow, 0, len(views)*2)
	for _, view := range views {
		lossRate := calcLossRate(view)
		lossStr := formatLossAgo(view.LastLossTime)
		rttStr := formatRTT(view.LastRTT)
		avgStr := formatRTT(view.AvgRTT)
		jitterStr := formatRTT(view.Jitter)
		mtuStr := mtuString(view.IfaceMTU)
		ttlStr := ttlString(view.LastTTL)
		rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
		errText := formatTableError(view.LastError)

		r1 := compactRow{
			hostL: view.Host,
			pathL: fmt.Sprintf("%s -> %s", rowSourceIP, view.IP),
			statL: fmt.Sprintf("S:%d L:%d Loss:%0.1f%% RTT:%s", view.Recv, view.Loss, lossRate, rttStr),
			errL:  errText,
		}
		r2 := compactRow{
			hostR: "Avg/Jit",
			pathR: "",
			statR: fmt.Sprintf("Avg:%s Jit:%s TTL:%s Sz:%d MTU:%s Last:%s", avgStr, jitterStr, ttlStr, packetSize, mtuStr, lossStr),
			errR:  "",
		}
		rows = append(rows, r1, r2)

		for i, v := range []string{r1.hostL, r1.pathL, r1.statL, r1.errL, r2.hostR, r2.pathR, r2.statR, r2.errR} {
			col := i % 4
			if w := runewidth.StringWidth(v); w > desired[col] {
				desired[col] = w
			}
		}
	}

	return compactLayout{
		rows:    rows,
		desired: desired,
		headers: headers,
		aligns:  aligns,
		min:     min,
		max:     max,
	}
}

func buildCompactRowCells(values []string, widths []int, aligns []int, rowColor tcell.Color) []*tview.TableCell {
	cells := make([]*tview.TableCell, len(values))
	for c, v := range values {
		cell := tview.NewTableCell(formatCellText(v, widths[c], aligns[c])).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(rowColor).
			SetAlign(aligns[c])
		if c == 0 {
			cell.SetTextColor(tcell.ColorWhite)
		} else if c == 3 && strings.TrimSpace(v) != "" {
			cell.SetTextColor(vividRed)
		}
		cells[c] = cell
	}
	return cells
}
