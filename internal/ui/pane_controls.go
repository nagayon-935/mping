package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type paneControl struct {
	title   string
	content tview.Primitive
	focus   tview.Primitive
	border  func(tcell.Color)
	folded  bool
	weight  int
	stub    *tview.TextView
}

type paneControls struct {
	app       *tview.Application
	layout    *tview.Flex
	header    *tview.TextView
	footer    *tview.Pages
	panes     []*paneControl
	maximized *paneControl
}

func newPaneControls(app *tview.Application, layout *tview.Flex, header *tview.TextView, footer *tview.Pages, table *tview.Table, tablePane *tview.Flex, monitors []*monitorPane, graph *GraphView, log *tview.TextView) *paneControls {
	c := &paneControls{app: app, layout: layout, header: header, footer: footer}
	add := func(title string, content, focus tview.Primitive, weight int, border func(tcell.Color)) {
		stub := tview.NewTextView().SetText(fmt.Sprintf("[+] %s  (f: Expand, z: Maximize)", title)).SetWrap(false)
		stub.SetBackgroundColor(tcell.ColorBlack)
		stub.SetTextColor(tcell.ColorYellow)
		c.panes = append(c.panes, &paneControl{title: title, content: content, focus: focus, weight: weight, border: border, stub: stub})
	}
	add("Ping Monitor", tablePane, table, 3, func(color tcell.Color) { tablePane.SetBorderColor(color) })
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
			add(pane.title, pane.pane, pane.view, weight, pane.setBorderColor)
		}
	}
	add("RTT Graphs", graph, graph, 3, func(color tcell.Color) { graph.SetBorderColor(color) })
	add("Log", log, log, 2, func(color tcell.Color) { log.SetBorderColor(color) })
	return c
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
		p.border(tcell.ColorWhite)
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

func (c *paneControls) rebuild() {
	c.layout.Clear().AddItem(c.header, 2, 0, false)
	if c.maximized != nil {
		c.layout.AddItem(c.maximized.content, 0, 1, true)
	} else {
		for _, pane := range c.panes {
			if pane.folded {
				c.layout.AddItem(pane.stub, 1, 0, false)
			} else {
				c.layout.AddItem(pane.content, 0, pane.weight, false)
			}
		}
	}
	c.layout.AddItem(c.footer, 1, 0, false)
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
		for i, p := range c.panes {
			if p == pane {
				c.focus(c.panes[(i+1)%len(c.panes)])
				return true
			}
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
