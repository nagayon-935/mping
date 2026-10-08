package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func testPaneControls() (*paneControls, *tview.Table, *GraphView) {
	app := tview.NewApplication()
	table := tview.NewTable()
	tablePane := tview.NewFlex().AddItem(table, 0, 1, true)
	graph := NewGraphView([]*stats.TargetStats{stats.NewTargetStats("host")}, time.Second)
	log := tview.NewTextView()
	header := tview.NewTextView()
	footer := tview.NewPages()
	monitor := newMonitorPane(true, " MTR Monitor ", func(int) string { return "data" })
	controls := newPaneControls(app, tview.NewFlex(), header, footer, table, tablePane, table.GetRowCount, []*monitorPane{monitor}, graph, log)
	app.SetFocus(table)
	controls.rebuild()
	return controls, table, graph
}

func TestFoldedPanesRemainReachableAndMaximizationRestoresState(t *testing.T) {
	c, table, graph := testPaneControls()
	graph.scrollRow = 2
	table.SetOffset(4, 0)
	key := func(r rune) {
		t.Helper()
		if !c.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)) {
			t.Fatalf("key %c not handled", r)
		}
	}
	key('f')
	if !c.panes[0].folded || c.app.GetFocus() != c.panes[0].stub {
		t.Fatal("folded pane cannot be focused")
	}
	key('z')
	if c.maximized != c.panes[0] || c.app.GetFocus() != table {
		t.Fatal("folded pane did not maximize")
	}
	if !c.handle(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)) {
		t.Fatal("escape ignored")
	}
	if c.maximized != nil || !c.panes[0].folded || c.app.GetFocus() != c.panes[0].stub {
		t.Fatal("folded state not restored")
	}
	key('f')
	row, _ := table.GetOffset()
	if row != 4 || graph.scrollRow != 2 {
		t.Fatal("display operation changed scroll state")
	}
	for range c.panes {
		key('f')
		c.handle(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	}
	for _, pane := range c.panes {
		if !pane.folded {
			t.Fatal("all-folded fixture failed")
		}
	}
	key('f')
	if c.current().folded {
		t.Fatal("cannot recover from all-folded layout")
	}
}

func TestPaneControlsLeaveTextInputsAndMeasurementKeysAlone(t *testing.T) {
	c, _, _ := testPaneControls()
	for _, r := range []rune{'s', 'S', 'R', 'q', 'a', 'd'} {
		if c.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)) {
			t.Fatalf("measurement key %c intercepted", r)
		}
	}
	c.app.SetFocus(tview.NewInputField())
	if c.handle(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModNone)) {
		t.Fatal("hostname input intercepted")
	}
}
