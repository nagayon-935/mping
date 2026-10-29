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
	parts := strings.Split(err, ": ")
	return parts[len(parts)-1]
}

func calcInitialTableErrorWidth(targets []*stats.TargetStats, header string, minWidth int) int {
	maxWidth := max(minWidth, runewidth.StringWidth(header))
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

// compactRow is one line of the compact layout: one string per column
// (Host, Path, Stats, Error).
type compactRow [4]string

type compactLayout struct {
	rows    []compactRow
	desired []int
	headers []string
	aligns  []int
	min     []int
	max     []int
	shrink  []int // shrink priority per column
	grow    []int // grow priority per column
}

func buildCompactLayout(views []stats.TargetView, packetSize int, sourceIPv4, sourceIPv6 string, errorMaxWidth int) compactLayout {
	cols := compactTableColumns()
	cols[len(cols)-1].max = errorMaxWidth // the Error column's max is data-derived
	layout := compactLayout{
		desired: make([]int, len(cols)),
		headers: make([]string, len(cols)),
		aligns:  make([]int, len(cols)),
		min:     make([]int, len(cols)),
		max:     make([]int, len(cols)),
		shrink:  make([]int, len(cols)),
		grow:    make([]int, len(cols)),
	}
	for i, c := range cols {
		layout.desired[i] = runewidth.StringWidth(c.name)
		layout.headers[i] = c.name
		layout.aligns[i] = c.align
		layout.min[i] = c.min
		layout.max[i] = c.max
		layout.shrink[i] = c.shrinkPriority
		layout.grow[i] = c.growPriority
	}

	rows := make([]compactRow, 0, len(views)*2)
	for _, view := range views {
		lossRate := calcLossRate(view)
		lossStr := formatLossAgo(view.LastLossTime)
		rttStr := formatRTT(view.LastRTT)
		avgStr := formatRTT(view.AvgRTT)
		jitterStr := formatRTT(view.Jitter)
		mtuStr := intOrDash(view.IfaceMTU)
		ttlStr := intOrDash(view.LastTTL)
		rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
		errText := formatTableError(view.LastError)

		r1 := compactRow{
			view.Host,
			fmt.Sprintf("%s -> %s", rowSourceIP, view.IP),
			fmt.Sprintf("S:%d L:%d Loss:%0.1f%% RTT:%s", view.Recv, view.Loss, lossRate, rttStr),
			errText,
		}
		r2 := compactRow{
			"Avg/Jit",
			"",
			fmt.Sprintf("Avg:%s Jit:%s TTL:%s Sz:%d MTU:%s Last:%s", avgStr, jitterStr, ttlStr, packetSize, mtuStr, lossStr),
			"",
		}
		rows = append(rows, r1, r2)

		for _, r := range [...]compactRow{r1, r2} {
			for col, v := range r {
				layout.desired[col] = max(layout.desired[col], runewidth.StringWidth(v))
			}
		}
	}

	layout.rows = rows
	return layout
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
