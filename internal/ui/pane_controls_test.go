package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func testPaneControls() (*paneControls, *tview.Table, *GraphView) {
	return newTestPaneControls(newMonitorPane(true, " MTR Monitor ", func(int) string { return "data" }))
}

// newTestPaneControls builds pane controls around the given monitor panes,
// with the table focused.
func newTestPaneControls(monitors ...*monitorPane) (*paneControls, *tview.Table, *GraphView) {
	app := tview.NewApplication()
	table := tview.NewTable()
	tablePane := tview.NewFlex().AddItem(table, 0, 1, true)
	graph := NewGraphView([]*stats.TargetStats{stats.NewTargetStats("host")}, time.Second)
	log := tview.NewTextView()
	header := tview.NewTextView()
	footer := tview.NewPages()
	controls := newPaneControls(app, tview.NewFlex(), header, footer, table, tablePane, table.GetRowCount, monitors, graph, log)
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

func TestPaneControlsTabRestoresUnfocusedBorderColors(t *testing.T) {
	c, _, graph := testPaneControls()
	// Cycle through all panes with Tab and ensure Graph and Log retain their
	// distinct unfocused border colors after focus leaves them.
	for range len(c.panes) {
		if !c.handle(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)) {
			t.Fatal("Tab not handled")
		}
	}
	if got := graph.GetBorderColor(); got != vividCyan {
		t.Errorf("graph unfocused border color = %v, want %v (vividCyan)", got, vividCyan)
	}
	if got := c.log.content.(*tview.TextView).GetBorderColor(); got != vividRed {
		t.Errorf("log unfocused border color = %v, want %v (vividRed)", got, vividRed)
	}
}

// TestPaneControlsTabVisitsEnabledPanesInOrder follows focus around the
// whole cycle for each monitor combination: the table, then the enabled
// monitors in order, the RTT graph, the Log, and back to the table.
func TestPaneControlsTabVisitsEnabledPanesInOrder(t *testing.T) {
	render := func(int) string { return "data" }
	trace := func(enabled bool) *monitorPane { return newMonitorPane(enabled, " Traceroute Monitor ", render) }
	port := func(enabled bool) *monitorPane { return newMonitorPane(enabled, " Port Monitor ", render) }
	tests := []struct {
		name     string
		monitors []*monitorPane
		want     []string
	}{
		{"no monitors", nil, []string{"RTT Graphs", "Log", "Ping Monitor"}},
		{"disabled monitors are skipped", []*monitorPane{trace(false), port(false)},
			[]string{"RTT Graphs", "Log", "Ping Monitor"}},
		{"traceroute only", []*monitorPane{trace(true), port(false)},
			[]string{" Traceroute Monitor ", "RTT Graphs", "Log", "Ping Monitor"}},
		{"port only", []*monitorPane{trace(false), port(true)},
			[]string{" Port Monitor ", "RTT Graphs", "Log", "Ping Monitor"}},
		{"traceroute and port", []*monitorPane{trace(true), port(true)},
			[]string{" Traceroute Monitor ", " Port Monitor ", "RTT Graphs", "Log", "Ping Monitor"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestPaneControls(tt.monitors...)
			if got := c.current().title; got != "Ping Monitor" {
				t.Fatalf("initial focus = %q, want the Ping Monitor", got)
			}
			var got []string
			for range tt.want {
				if !c.handle(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)) {
					t.Fatal("Tab not handled")
				}
				got = append(got, c.current().title)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("focus order after Tab = %q, want %q", got, tt.want)
			}
		})
	}
}
