package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// monitorPane bundles construction, per-tick rendering, and focus-cycle
// wiring for one of the optional side panels (Traceroute or MTR, Port and
// HTTP Monitor), so they share one constructor and the same render and
// focus-cycle methods.
type monitorPane struct {
	title string
	// titleFn, when set, replaces title on every draw (the route pane is
	// renamed when its mode switches).
	titleFn func() string
	enabled bool
	view    *tview.TextView
	pane    *tview.Flex
	// setBorderColor is always callable (a no-op when the pane is disabled),
	// so the focus cycle doesn't need a nil check at each use site.
	setBorderColor func(tcell.Color)
	// render returns this pane's text content for the given available
	// width; called once per refresh tick.
	render func(availW int) string
	// lastText caches the most recently applied SetText content so refresh
	// can skip the call (and tview's internal re-tagging/re-wrap) when
	// render() produces byte-for-byte the same string as last tick. render()
	// itself still runs every tick — this only saves the tview-side reparse,
	// which is why several panes (those that embed a live "N ago" timestamp)
	// won't hit this cache on most ticks.
	lastText string
	// lines is the line count of lastText; the layout sizes the pane to it.
	lines int
}

// newMonitorPane constructs a monitor pane. When enabled is false, view and
// pane stay nil; refresh() and layout assembly skip it accordingly.
func newMonitorPane(enabled bool, title string, render func(availW int) string) *monitorPane {
	mp := &monitorPane{title: title, enabled: enabled, render: render, setBorderColor: func(tcell.Color) {}}
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
	pane.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		return makeDoubleBorderDrawFunc(mp.currentTitle(), &borderColor)(screen, x, y, width, height)
	})

	mp.view = view
	mp.pane = pane
	return mp
}

// currentTitle is the title the pane's border shows now.
func (mp *monitorPane) currentTitle() string {
	if mp.titleFn != nil {
		return mp.titleFn()
	}
	return mp.title
}

// refresh re-renders the pane's content in place. No-op when disabled.
// Skips the SetText call (but still calls render()) when the content is
// unchanged since the last tick, to avoid tview re-parsing identical text.
func (mp *monitorPane) refresh() {
	if !mp.enabled || mp.view == nil {
		return
	}
	_, _, availW, _ := mp.view.GetInnerRect()
	text := mp.render(availW)
	if text == mp.lastText {
		return
	}
	mp.lastText = text
	mp.lines = textLineCount(text)
	mp.view.SetText(text)
}
