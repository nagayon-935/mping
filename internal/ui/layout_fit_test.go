package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestFitHeights(t *testing.T) {
	tests := []struct {
		name      string
		avail     int
		demands   []paneDemand
		want      []int
		wantSpare int
	}{
		{
			name:  "short panes shrink and the rest goes to the pane that needs it",
			avail: 100,
			demands: []paneDemand{
				{weight: 3, desired: 13}, // ping
				{weight: 2, desired: 13}, // traceroute
				{weight: 2, desired: 80}, // mtr
				{weight: 3, desired: 24}, // graph
				{weight: 2, desired: 5},
			},
			want: []int{13, 13, 45, 24, 5},
		},
		{
			name:  "rows nobody needs are reported as spare",
			avail: 100,
			demands: []paneDemand{
				{weight: 3, desired: 13},
				{weight: 3, desired: 24},
			},
			want:      []int{13, 24},
			wantSpare: 63,
		},
		{
			name:  "panes that all overflow split by weight",
			avail: 50,
			demands: []paneDemand{
				{weight: 3, desired: 100},
				{weight: 2, desired: 100},
			},
			want: []int{30, 20},
		},
		{
			name:  "empty content still keeps the minimum height",
			avail: 40,
			demands: []paneDemand{
				{weight: 1, desired: 0},
				{weight: 1, desired: 100},
			},
			want: []int{paneMinHeight, 40 - paneMinHeight},
		},
		{
			name:      "no panes leaves everything spare",
			avail:     10,
			demands:   nil,
			want:      []int{},
			wantSpare: 10,
		},
		{
			name:    "no space",
			avail:   0,
			demands: []paneDemand{{weight: 1, desired: 10}},
			want:    []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, spare := fitHeights(tt.avail, tt.demands)
			if !reflect.DeepEqual(got, tt.want) || spare != tt.wantSpare {
				t.Fatalf("fitHeights() = %v, spare %d; want %v, spare %d", got, spare, tt.want, tt.wantSpare)
			}
			sum := spare
			for _, h := range got {
				sum += h
			}
			if tt.avail > 0 && sum != tt.avail {
				t.Fatalf("heights plus spare sum to %d, want %d", sum, tt.avail)
			}
		})
	}
}

func TestTextLineCount(t *testing.T) {
	tests := map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "\n\n": 2}
	for in, want := range tests {
		if got := textLineCount(in); got != want {
			t.Errorf("textLineCount(%q) = %d, want %d", in, got, want)
		}
	}
}

// fitFixture builds pane controls with 4 targets, a 9-line traceroute pane
// and an MTR pane of mtrLines lines, then draws them on a 200x150 screen.
type fitFixture struct {
	controls *paneControls
	table    *tview.Flex
	trace    *monitorPane
	mtr      *monitorPane
	graph    *GraphView
	log      *tview.TextView
	vs       *viewState
}

func newFitFixture(t *testing.T, mtrLines int) *fitFixture {
	t.Helper()
	targets := []*stats.TargetStats{
		stats.NewTargetStats("a"), stats.NewTargetStats("b"),
		stats.NewTargetStats("c"), stats.NewTargetStats("d"),
	}
	table := tview.NewTable()
	tablePane := tview.NewFlex().AddItem(table, 0, 1, true)
	trace := newMonitorPane(true, " Traceroute Monitor ", func(int) string { return strings.Repeat("hop\n", 9) })
	mtr := newMonitorPane(true, " MTR Monitor ", func(int) string { return strings.Repeat("hop\n", mtrLines) })
	trace.refresh()
	mtr.refresh()
	graph := NewGraphView(targets, time.Second)
	log := newLogView()
	layout := tview.NewFlex().SetDirection(tview.FlexRow)
	c := newPaneControls(tview.NewApplication(), layout, tview.NewTextView(), tview.NewPages(), table, tablePane,
		func() int { return len(targets) + 1 }, []*monitorPane{trace, mtr}, graph, log)
	c.rebuild()
	return &fitFixture{controls: c, table: tablePane, trace: trace, mtr: mtr, graph: graph, log: log, vs: newViewState(log)}
}

func (f *fitFixture) draw(t *testing.T) {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(200, 150)
	view := f.controls.view()
	view.SetRect(0, 0, 200, 150)
	view.Draw(screen)
}

func paneHeight(p tview.Primitive) int {
	_, _, _, h := p.GetRect()
	return h
}

const fixedLogHeight = logLines + 2

func TestPaneControlsFitLeavesNoBlankRows(t *testing.T) {
	f := newFitFixture(t, 200)

	f.draw(t)

	if got := paneHeight(f.table); got != 13 {
		t.Errorf("ping pane height = %d, want 13 (5 bordered rows + pane border)", got)
	}
	if got := paneHeight(f.trace.pane); got != 11 {
		t.Errorf("traceroute pane height = %d, want 11", got)
	}
	if got := paneHeight(f.log); got != fixedLogHeight {
		t.Errorf("log pane height = %d, want %d", got, fixedLogHeight)
	}
	if got, want := paneHeight(f.mtr.pane), 150-3-13-11-fixedLogHeight-f.graph.preferredHeight(198); got != want {
		t.Errorf("mtr pane height = %d, want %d (all space the others leave)", got, want)
	}
}

func TestPaneControlsFitLogGrowthDoesNotShrinkOtherPanes(t *testing.T) {
	f := newFitFixture(t, 200)
	f.draw(t)
	mtrBefore, graphBefore := paneHeight(f.mtr.pane), paneHeight(f.graph)

	for i := range 80 {
		f.vs.appendLog(fmt.Sprintf("message %d", i))
	}
	f.draw(t)

	if got := paneHeight(f.log); got != fixedLogHeight {
		t.Errorf("log pane height = %d, want %d", got, fixedLogHeight)
	}
	if got := paneHeight(f.mtr.pane); got != mtrBefore {
		t.Errorf("mtr pane height = %d after logging, want %d", got, mtrBefore)
	}
	if got := paneHeight(f.graph); got != graphBefore {
		t.Errorf("graph height = %d after logging, want %d", got, graphBefore)
	}
}

func TestPaneControlsFitLogGrowsOnlyIntoSpareRows(t *testing.T) {
	// Every pane fits, so rows are left over for the log and the graph.
	f := newFitFixture(t, 20)
	for i := range 12 {
		f.vs.appendLog(fmt.Sprintf("message %d", i))
	}

	f.draw(t)

	if got := paneHeight(f.mtr.pane); got != 22 {
		t.Errorf("mtr pane height = %d, want its content height 22", got)
	}
	if got := paneHeight(f.log); got != 12+2 {
		t.Errorf("log pane height = %d, want its content height %d", got, 12+2)
	}
	if got, want := paneHeight(f.graph), 150-3-13-11-22-(12+2); got != want {
		t.Errorf("graph height = %d, want the remaining %d rows", got, want)
	}
}

func TestPaneControlsFitGivesSpareRowsToGraph(t *testing.T) {
	table := tview.NewTable()
	tablePane := tview.NewFlex().AddItem(table, 0, 1, true)
	graph := NewGraphView([]*stats.TargetStats{stats.NewTargetStats("a")}, time.Second)
	log := tview.NewTextView()
	layout := tview.NewFlex().SetDirection(tview.FlexRow)
	c := newPaneControls(tview.NewApplication(), layout, tview.NewTextView(), tview.NewPages(), table, tablePane,
		func() int { return 2 }, nil, graph, log)
	c.rebuild()
	view := c.view()
	view.SetRect(0, 0, 120, 60)
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	view.Draw(screen)
	if got, want := paneHeight(graph), 60-3-7-fixedLogHeight; got != want {
		t.Errorf("graph height = %d, want %d", got, want)
	}
}
