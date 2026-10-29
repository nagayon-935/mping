package ui

import (
	"fmt"
	"slices"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Fixed rows of the main layout around the panes.
const (
	headerHeight = 2
	footerHeight = 1
)

type paneControl struct {
	title string
	// titleFn, when set, renames the folded stub as the title changes.
	titleFn        func() string
	content        tview.Primitive
	focus          tview.Primitive
	border         func(tcell.Color)
	unfocusedColor tcell.Color
	folded         bool
	weight         int
	stub           *tview.TextView
	// desired returns the pane's content height (borders included) for the
	// given layout width; fit sizes the pane to it when space allows.
	desired func(width int) int
}

type paneControls struct {
	app           *tview.Application
	layout        *tview.Flex
	header        *tview.TextView
	footer        *tview.Pages
	table         *tview.Table
	tablePane     *tview.Flex
	onTableResize func()
	panes         []*paneControl
	maximized     *paneControl
	// graph and log get special treatment in fit: the Log keeps a fixed
	// height and grows only into spare rows, and the graph takes whatever
	// rows remain after that.
	graph *paneControl
	log   *paneControl
}

// newPaneControls wires the fold/maximize controls and the content-fitted
// layout. tableRows reports the Ping Monitor table's logical row count
// (header included).
func newPaneControls(app *tview.Application, layout *tview.Flex, header *tview.TextView, footer *tview.Pages, table *tview.Table, tablePane *tview.Flex, tableRows func() int, monitors []*monitorPane, graph *GraphView, log *tview.TextView) *paneControls {
	c := &paneControls{app: app, layout: layout, header: header, footer: footer, table: table, tablePane: tablePane}
	add := func(title string, content, focus tview.Primitive, weight int, unfocusedColor tcell.Color, border func(tcell.Color), desired func(width int) int) *paneControl {
		stub := tview.NewTextView().SetText(stubText(title)).SetWrap(false)
		stub.SetBackgroundColor(tcell.ColorBlack)
		stub.SetTextColor(tcell.ColorYellow)
		pane := &paneControl{title: title, content: content, focus: focus, weight: weight, unfocusedColor: unfocusedColor, border: border, stub: stub, desired: desired}
		c.panes = append(c.panes, pane)
		return pane
	}
	// Bordered table: a top border plus each row and the rule below it,
	// inside the pane's own border.
	add("Ping Monitor", tablePane, table, 3, tcell.ColorWhite, func(color tcell.Color) { tablePane.SetBorderColor(color) },
		func(int) int { return 2*max(tableRows(), 1) + 3 })
	active := 0
	for _, pane := range monitors {
		if pane.enabled {
			active++
		}
	}
	weight := 3
	if active > 1 {
		weight = 2
	}
	for _, pane := range monitors {
		if pane.enabled {
			mp := pane
			pane := add(mp.title, mp.pane, mp.view, weight, tcell.ColorWhite, mp.setBorderColor,
				func(int) int { return mp.lines + 2 })
			pane.titleFn = mp.titleFn
		}
	}
	c.graph = add("RTT Graphs", graph, graph, 3, vividCyan, func(color tcell.Color) { graph.SetBorderColor(color) },
		func(width int) int { return graph.preferredHeight(width - 2) })
	c.log = add("Log", log, log, 2, vividRed, func(color tcell.Color) { log.SetBorderColor(color) },
		func(int) int { return max(logContentLines(log), logLines) + 2 })
	return c
}

// view returns the layout primitive that refits pane heights on each draw.
func (c *paneControls) view() tview.Primitive {
	return &fitFlex{Flex: c.layout, fit: c.fit}
}

func (c *paneControls) syncTableRect(width, paneHeight int) {
	if c.tablePane == nil || width <= 0 || paneHeight <= 0 {
		return
	}
	x, y, _, _ := c.tablePane.GetRect()
	c.tablePane.SetRect(x, y, width, paneHeight)
	if c.table != nil {
		c.table.SetRect(x+1, y+1, max(width-2, 0), max(paneHeight-2, 0))
	}
	if c.onTableResize != nil {
		c.onTableResize()
	}
}

// fit sizes the unfolded panes for a layout of the given inner size. The
// Log keeps logLines of content; the other panes share the rest by content
// and weight (see fitHeights). Rows nobody needs go first to the Log, up to
// its content, then to the RTT graph. A maximized pane keeps the whole area.
func (c *paneControls) fit(width, height int) {
	for _, pane := range c.panes {
		if pane.titleFn != nil && pane.folded {
			if text := stubText(pane.titleFn()); pane.stub.GetText(false) != text {
				pane.stub.SetText(text)
			}
		}
	}
	if c.maximized != nil {
		if len(c.panes) > 0 && c.maximized == c.panes[0] {
			c.syncTableRect(width, max(height-headerHeight-footerHeight, 0))
		}
		return
	}
	avail := height - headerHeight - footerHeight
	var others []*paneControl
	logOpen := false
	for _, pane := range c.panes {
		switch {
		case pane.folded:
			avail--
		case pane == c.log:
			logOpen = true
		default:
			others = append(others, pane)
		}
	}
	logBase := 0
	if logOpen {
		logBase = min(logLines+2, max(avail, 0))
		avail -= logBase
	}
	computeSizes := func() ([]int, int) {
		logSize := logBase
		demands := make([]paneDemand, len(others))
		for i, pane := range others {
			demands[i] = paneDemand{weight: pane.weight, desired: pane.desired(width)}
		}
		sizes, spare := fitHeights(avail, demands)
		if logOpen {
			grow := min(spare, max(c.log.desired(width)-logSize, 0))
			logSize += grow
			spare -= grow
		}
		if spare > 0 {
			switch i := slices.Index(others, c.graph); {
			case i >= 0:
				sizes[i] += spare
			case logOpen:
				logSize += spare
			case len(others) > 0:
				sizes[len(others)-1] += spare
			}
		}
		return sizes, logSize
	}
	sizes, logSize := computeSizes()
	if len(others) > 0 && len(c.panes) > 0 && others[0] == c.panes[0] {
		desiredBefore := c.panes[0].desired(width)
		c.syncTableRect(width, sizes[0])
		if c.panes[0].desired(width) != desiredBefore {
			sizes, logSize = computeSizes()
			c.syncTableRect(width, sizes[0])
		}
	}
	for i, size := range sizes {
		c.layout.ResizeItem(others[i].content, size, 0)
	}
	if logOpen {
		c.layout.ResizeItem(c.log.content, logSize, 0)
	}
}

// logContentLines is the Log's wrapped line count without the empty line
// after the final newline that appendErrorLog ends every message with.
func logContentLines(log *tview.TextView) int {
	return max(log.GetWrappedLineCount()-1, 0)
}

func (c *paneControls) current() *paneControl {
	focus := c.app.GetFocus()
	for _, pane := range c.panes {
		if focus == pane.focus || focus == pane.stub {
			return pane
		}
	}
	return nil
}

func (c *paneControls) focus(pane *paneControl) {
	for _, p := range c.panes {
		p.border(p.unfocusedColor)
		p.stub.SetTextColor(tcell.ColorYellow)
	}
	pane.border(tcell.ColorGreen)
	pane.stub.SetTextColor(tcell.ColorGreen)
	if pane.folded && c.maximized != pane {
		c.app.SetFocus(pane.stub)
	} else {
		c.app.SetFocus(pane.focus)
	}
}

// stubText is what a folded pane shows in place of its content.
func stubText(title string) string {
	return fmt.Sprintf("[+] %s  (f: Expand, z: Maximize)", title)
}

func (c *paneControls) rebuild() {
	c.layout.Clear().AddItem(c.header, headerHeight, 0, false)
	if c.maximized != nil {
		c.layout.AddItem(c.maximized.content, 0, 1, true)
	} else {
		for i, pane := range c.panes {
			if pane.folded {
				c.layout.AddItem(pane.stub, 1, 0, false)
			} else {
				// The first pane (Ping Monitor) takes the initial focus.
				c.layout.AddItem(pane.content, 0, pane.weight, i == 0)
			}
		}
	}
	c.layout.AddItem(c.footer, footerHeight, 0, false)
}

// Handles display operations only; measurement callbacks are never involved.
func (c *paneControls) handle(event *tcell.EventKey) bool {
	pane := c.current()
	if pane == nil {
		return false
	}
	if event.Key() == tcell.KeyEscape && c.maximized != nil {
		original := c.maximized
		c.maximized = nil
		c.rebuild()
		c.focus(original)
		return true
	}
	if event.Key() == tcell.KeyTab {
		if c.maximized != nil {
			return true
		}
		if i := slices.Index(c.panes, pane); i >= 0 {
			c.focus(c.panes[(i+1)%len(c.panes)])
			return true
		}
	}
	switch event.Rune() {
	case 'f':
		c.maximized = nil
		pane.folded = !pane.folded
		c.rebuild()
		c.focus(pane)
		return true
	case 'z':
		if c.maximized == pane {
			c.maximized = nil
		} else {
			c.maximized = pane
		}
		c.rebuild()
		c.focus(pane)
		return true
	}
	return false
}
