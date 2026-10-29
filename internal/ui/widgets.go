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

// footerHelpText is the key help shown in the footer while measurement runs.
const footerHelpText = "Enter Detail | Tab Pane | f Fold | z Max | w Save | a Add | d Del | s Stop | q Quit"

func newFooter() *tview.TextView {
	footer := tview.NewTextView().
		SetText(footerHelpText).
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

// newSidePanes builds the route (Traceroute or MTR), Port and HTTP monitor
// panes. currentTargets is read on every render, so hosts added or removed
// after startup show up without rebuilding the panes.
func newSidePanes(opts RunOptions, currentTargets func() []*stats.TargetStats, vs *viewState) []*monitorPane {
	title := " Traceroute Monitor "
	if opts.Route == RouteMonitor {
		title = " MTR Monitor "
	}
	route := newMonitorPane(opts.Route != RouteOff, title, func(availW int) string {
		if opts.Route == RoutePath {
			return renderTracerouteTable(currentTargets(), availW)
		}
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
		return renderHTTPMonitorTable(results, vs.thresholds, availW, vs.lastHTTPStatuses, &vs.errorLogs, vs.errorView)
	})
	return []*monitorPane{route, port, http}
}

func newStyledModal(title, message string, buttons []string, done func(index int, label string)) *tview.Modal {
	modal := tview.NewModal().
		SetBackgroundColor(tcell.ColorBlack).
		SetTextColor(tcell.ColorWhite).
		SetButtonStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)).
		SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite)).
		SetText(message).
		AddButtons(buttons).
		SetFocus(0).
		SetDoneFunc(done)
	modal.Box.SetBackgroundColor(tcell.ColorBlack)
	modal.SetTitle(title).SetTitleColor(tcell.ColorWhite).SetBorderColor(tcell.ColorWhite)
	return modal
}

func handleModalKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab, tcell.KeyBacktab, tcell.KeyLeft, tcell.KeyRight, tcell.KeyEnter, tcell.KeyEscape, tcell.KeyCtrlC:
		return event
	}
	return nil
}
