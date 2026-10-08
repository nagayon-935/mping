package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// The constructors below build Run's widgets with their fixed styling, so Run
// itself only wires them together.

func newPingTable() *tview.Table {
	table := tview.NewTable().
		SetBorders(true).
		SetSelectable(false, false).
		SetFixed(1, 1)
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetBorderColor(tcell.ColorWhite)
	table.SetBordersColor(tcell.ColorWhite)
	return table
}

func newTablePane(table *tview.Table) *tview.Flex {
	pane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true)
	pane.SetBorder(true).SetTitle(" Ping Monitor ").SetBorderColor(tcell.ColorWhite)
	return pane
}

func newGraphPane(targets []*stats.TargetStats, interval time.Duration) *GraphView {
	graphView := NewGraphView(targets, interval)
	graphView.SetBorder(true).SetTitle(" RTT Graphs ").SetTitleColor(vividCyan).SetBorderColor(vividCyan)
	graphView.SetBackgroundColor(tcell.ColorBlack)
	return graphView
}

func newLogView() *tview.TextView {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true) // long messages wrap rather than run off the pane
	view.SetBorder(true).SetTitle(" Log ").SetTitleColor(vividRed).SetBorderColor(vividRed)
	view.SetBackgroundColor(tcell.ColorBlack)
	return view
}

func newHeader(interval time.Duration) *tview.TextView {
	header := tview.NewTextView().
		SetText(fmt.Sprintf("MPING - Multi Ping Tool | Interval: %dms", interval.Milliseconds())).
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorGreen).
		SetWrap(false)
	header.SetBackgroundColor(tcell.ColorBlack)
	return header
}

func newFooter() *tview.TextView {
	footer := tview.NewTextView().
		SetText("Enter Detail | Tab Pane | f Fold | z Max | w Save | a Add | d Del | s Stop | q Quit").
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorYellow).
		SetWrap(false)
	footer.SetBackgroundColor(tcell.ColorBlack)
	return footer
}

// newHostInput is the add/delete host prompt shown in the footer row.
func newHostInput(label string, labelColor tcell.Color) *tview.InputField {
	return tview.NewInputField().
		SetLabel(label).
		SetFieldBackgroundColor(tcell.ColorBlack).
		SetFieldTextColor(tcell.ColorWhite).
		SetLabelColor(labelColor)
}

// newSidePanes builds the Traceroute, MTR, Port and HTTP monitor panes.
// currentTargets is read on every render, so hosts added or removed after
// startup show up without rebuilding the panes.
func newSidePanes(opts RunOptions, currentTargets func() []*stats.TargetStats, vs *viewState) []*monitorPane {
	trace := newMonitorPane(opts.TraceEnabled, " Traceroute Monitor ", func(availW int) string {
		return renderTracerouteTable(currentTargets(), availW)
	})
	mtr := newMonitorPane(opts.MTREnabled, " MTR Monitor ", func(availW int) string {
		return renderMTRTable(currentTargets(), availW, opts.SourceIPv4, opts.SourceIPv6)
	})
	port := newMonitorPane(opts.PortEnabled, " Port Monitor ", func(availW int) string {
		return renderPortMonitorTable(currentTargets(), availW, vs.lastPortStatuses, &vs.errorLogs, vs.errorView)
	})
	http := newMonitorPane(opts.HTTPEnabled, " HTTP Monitor ", func(availW int) string {
		var results []*stats.HTTPCheckResult
		if opts.HTTPResults != nil {
			results = opts.HTTPResults()
		}
		return renderHTTPMonitorTable(results, availW, vs.lastHTTPStatuses, &vs.errorLogs, vs.errorView)
	})
	return []*monitorPane{trace, mtr, port, http}
}
