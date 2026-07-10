package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// monitorPane bundles construction, per-tick rendering, and focus-cycle
// wiring for one of the four optional side panels (Traceroute/MTR/Port/HTTP
// Monitor). TD-23①: replaces four near-identical ~20-line construction
// blocks in Run(), plus their separately hand-maintained render/focus-cycle
// /layout-assembly call sites, with one constructor and two methods shared
// by all four.
type monitorPane struct {
	enabled bool
	view    *tview.TextView
	pane    *tview.Flex
	// setBorderColor is always callable (a no-op when the pane is disabled),
	// so the focus cycle doesn't need a nil check at each use site.
	setBorderColor func(tcell.Color)
	// render returns this pane's text content for the given available
	// width; called once per refresh tick.
	render func(availW int) string
}

// newMonitorPane constructs a monitor pane. When enabled is false, view and
// pane stay nil; refresh() and layout assembly skip it accordingly.
func newMonitorPane(enabled bool, title string, render func(availW int) string) *monitorPane {
	mp := &monitorPane{enabled: enabled, render: render, setBorderColor: func(tcell.Color) {}}
	if !enabled {
		return mp
	}

	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	view.SetBackgroundColor(tcell.ColorBlack)

	pane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(view, 0, 1, true)
	pane.SetBorder(true).SetBorderColor(tcell.ColorWhite)

	borderColor := tcell.ColorWhite
	mp.setBorderColor = func(c tcell.Color) {
		borderColor = c
		pane.SetBorderColor(c)
	}
	pane.SetDrawFunc(makeDoubleBorderDrawFunc(title, &borderColor))

	mp.view = view
	mp.pane = pane
	return mp
}

// refresh re-renders the pane's content in place. No-op when disabled.
func (mp *monitorPane) refresh() {
	if !mp.enabled || mp.view == nil {
		return
	}
	_, _, availW, _ := mp.view.GetInnerRect()
	mp.view.SetText(mp.render(availW))
}
