package ui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestSelectionKeepsIdentityAndSkipsGroupHeadings(t *testing.T) {
	a, b, c := stats.NewTargetStats("same"), stats.NewTargetStats("same"), stats.NewTargetStats("ungrouped")
	table := tview.NewTable().SetBorders(true)
	pane := tview.NewFlex().AddItem(table, 0, 1, true)
	pane.SetRect(0, 0, 220, 40)
	table.SetRect(0, 0, 218, 38)
	tr := newTableRenderer([]*stats.TargetStats{a, b, c}, "", "", 56, false, false, false, []TargetGroup{{Name: "group", Indices: []int{0, 1}}}, table, pane, nil, newViewState(tview.NewTextView()))
	tr.selectionEnabled = true
	tr.selectedID = c.ID
	tr.update()
	tr.moveSelection(tcell.KeyDown)
	if tr.selectedID != a.ID {
		t.Fatal("selection did not follow displayed group order")
	}
	tr.moveSelection(tcell.KeyDown)
	if tr.selectedID != b.ID {
		t.Fatal("duplicate names conflated")
	}
	tr.targets = []*stats.TargetStats{c, b, a}
	tr.groups = nil
	tr.update()
	if tr.selectedID != b.ID {
		t.Fatal("selection changed during reorder")
	}
	tr.targets = []*stats.TargetStats{c, a}
	tr.update()
	if tr.selectedTarget() == nil || tr.selectedID == b.ID {
		t.Fatal("removed selection remained active")
	}
	pane.SetRect(0, 0, 70, 30)
	table.SetRect(0, 0, 68, 28)
	tr.update()
	if !tr.compactLayout {
		t.Fatal("fixture did not enter compact layout")
	}
	tr.moveSelection(tcell.KeyDown)
	if tr.selectedID != a.ID || tr.selectedRow() != 3 {
		t.Fatal("compact two-row selection incorrect")
	}
}

func TestDetailsOnlyShowsSelectedTargetEvents(t *testing.T) {
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	a.RecordEvent("ping", "A-only failure")
	b.RecordEvent("port", "B-only event")
	a.SetIP("127.0.0.1")
	a.SetIP("127.0.0.2")
	text := renderHostDetails(a, RunOptions{Interval: time.Second, Timeout: time.Second, PacketSize: 56})
	if !strings.Contains(text, "A-only failure") || strings.Contains(text, "B-only event") || !strings.Contains(text, "changed 1 times") {
		t.Fatalf("incorrect detail content: %s", text)
	}
}

func TestDetailsSimulationSelectsDuplicateAndReturnsAfterDeletion(t *testing.T) {
	previous := newApplication
	t.Cleanup(func() { newApplication = previous })
	frames := make(chan string, 32)
	screens := make(chan tcell.SimulationScreen, 1)
	newApplication = func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		app.SetScreen(screen)
		screen.SetSize(140, 45)
		app.SetAfterDrawFunc(func(screen tcell.Screen) {
			var b strings.Builder
			w, h := screen.Size()
			for y := 0; y < h; y++ {
				b.WriteString(screenRowString(screen, y, w))
				b.WriteByte('\n')
			}
			select {
			case frames <- b.String():
			default:
			}
		})
		screens <- screen
		return app
	}
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	b.RecordEvent("ping", "Selected B event")
	type set struct{ targets []*stats.TargetStats }
	var source atomic.Pointer[set]
	source.Store(&set{[]*stats.TargetStats{a, b}})
	deleted := make(chan uint64, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(RunOptions{Targets: []*stats.TargetStats{a, b}, Interval: 50 * time.Millisecond, Timeout: time.Second, PacketSize: 56, TargetSource: func() TargetSet { return TargetSet{Targets: source.Load().targets} }, OnDeleteTarget: func(id uint64) error { deleted <- id; source.Store(&set{[]*stats.TargetStats{a}}); return nil }})
	}()
	screen := <-screens
	t.Cleanup(func() { screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone) })
	wait := func(marker string) {
		t.Helper()
		timeout := time.NewTimer(3 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case frame := <-frames:
				if strings.Contains(frame, marker) {
					return
				}
			case <-timeout.C:
				t.Fatalf("frame missing %q", marker)
			}
		}
	}
	wait("same")
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	wait(fmt.Sprintf("Target #%d", b.ID))
	screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
	wait("Selected B event")
	screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
	select {
	case id := <-deleted:
		if id != b.ID {
			t.Fatal("deleted wrong duplicate")
		}
	case <-time.After(time.Second):
		t.Fatal("delete callback missing")
	}
	wait("Ping Monitor")
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("UI did not exit")
	}
}
