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
	// onGraphShown is told whether the RTT graph pane is open, whenever that
	// changes: the Ping Monitor shows its sparkline column only while the
	// graphs are folded.
	onGraphShown func(shown bool)
	// graphManual is set once the user folds or opens the RTT graph pane;
	// until then fit opens it only while every series gets its chart.
	graphManual bool
	// graphNeed is the RTT graph pane height (borders included) at which
	// every series gets its chart, or 0 when they never all fit at once.
	graphNeed func(width int) int
	// graphMax is the tallest the open RTT graph pane grows to (borders
	// included); fit leaves rows past it to the Ping Monitor.
	graphMax                      func(width int) int
	graphNotified, graphShownLast bool
	panes                         []*paneControl
	maximized                     *paneControl
	// graph and log get special treatment in fit: the Log keeps a fixed
	// height, the open graph takes spare rows up to graphMax, and the Log
	// grows into spare rows only while the graph is folded.
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
	// One line per table row (header included) inside the pane's border.
	add("Ping Monitor", tablePane, table, 3, tcell.ColorWhite, func(color tcell.Color) { tablePane.SetBorderColor(color) },
		func(int) int { return max(tableRows(), 1) + 2 })
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
	c.graphNeed = func(width int) int { return graph.fullHeight(width - 2) }
	c.graphMax = func(width int) int { return graph.maxHeight(width - 2) }
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
// and weight (see fitHeights). Rows nobody needs make the open RTT charts
// taller up to graphMax, then go to the Ping Monitor; while
// the charts are folded the Log grows into them instead. A maximized pane
// keeps the whole area.
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
	c.autoFoldGraph(width, height)
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
		// While the RTT charts are open the spare rows go to them and the
		// Ping Monitor; the Log grows to its content only when they are
		// folded.
		graphOpen := slices.Index(others, c.graph) >= 0
		if logOpen && !graphOpen {
			grow := min(spare, max(c.log.desired(width)-logSize, 0))
			logSize += grow
			spare -= grow
		}
		if i := slices.Index(others, c.graph); i >= 0 && spare > 0 {
			// The open graphs grow up to graphMax; rows past that go to the
			// Ping Monitor (blank rows below the hosts), so the charts do not
			// dominate a tall terminal and the Log stays at its base height.
			limit := c.graph.desired(width)
			if c.graphMax != nil {
				limit = c.graphMax(width)
			}
			grow := min(spare, max(limit-sizes[i], 0))
			sizes[i] += grow
			spare -= grow
			if spare > 0 && len(c.panes) > 0 && others[0] == c.panes[0] {
				sizes[0] += spare
				spare = 0
			}
		}
		if spare > 0 {
			switch {
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

// autoFoldGraph opens the RTT graph pane only while every series gets its
// chart (see GraphView.fullHeight) in the rows the other panes leave (each at its content height, the
// Log at its base), and folds it otherwise, until the user folds or opens it
// with 'f'. The inputs are pane content heights (host count, layout size),
// not live values, so the state does not flip while data updates.
func (c *paneControls) autoFoldGraph(width, height int) {
	if c.graph == nil {
		return
	}
	if !c.graphManual {
		room := height - headerHeight - footerHeight
		for _, pane := range c.panes {
			switch {
			case pane == c.graph:
			case pane.folded:
				room--
			case pane == c.log:
				room -= logLines + 2
			default:
				room -= max(pane.desired(width), paneMinHeight)
			}
		}
		need := c.graph.desired(width)
		if c.graphNeed != nil {
			need = c.graphNeed(width)
		}
		if fold := need == 0 || room < need; fold != c.graph.folded {
			// fit runs inside Draw, which holds the application lock:
			// query focus through the primitives, and move it after the draw.
			focused := c.graph.focus.HasFocus() || c.graph.stub.HasFocus()
			c.graph.folded = fold
			c.rebuild()
			if focused {
				go c.app.QueueUpdateDraw(func() { c.focus(c.graph) })
			}
		}
	}
	if shown := !c.graph.folded; !c.graphNotified || shown != c.graphShownLast {
		c.graphNotified, c.graphShownLast = true, shown
		if c.onGraphShown != nil {
			c.onGraphShown(shown)
		}
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

// handle processes display operations only; measurement callbacks are never involved.
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
		if pane == c.graph {
			c.graphManual = true
		}
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
