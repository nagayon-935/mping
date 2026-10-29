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
	tr := newTableRenderer([]*stats.TargetStats{a, b, c}, "", "", 56, false, false, []TargetGroup{{Name: "group", Indices: []int{0, 1}}}, table, pane, nil, newViewState(tview.NewTextView()))
	tr.selectionEnabled = true
	tr.selectedID = c.ID()
	tr.update()
	tr.moveSelection(tcell.KeyDown)
	if tr.selectedID != a.ID() {
		t.Fatal("selection did not follow displayed group order")
	}
	tr.moveSelection(tcell.KeyDown)
	if tr.selectedID != b.ID() {
		t.Fatal("duplicate names conflated")
	}
	tr.targets = []*stats.TargetStats{c, b, a}
	tr.groups = nil
	tr.update()
	if tr.selectedID != b.ID() {
		t.Fatal("selection changed during reorder")
	}
	tr.targets = []*stats.TargetStats{c, a}
	tr.update()
	if tr.selectedTarget() == nil || tr.selectedID == b.ID() {
		t.Fatal("removed selection remained active")
	}
	pane.SetRect(0, 0, 70, 30)
	table.SetRect(0, 0, 68, 28)
	tr.update()
	if !tr.compactLayout {
		t.Fatal("fixture did not enter compact layout")
	}
	tr.moveSelection(tcell.KeyDown)
	if tr.selectedID != a.ID() || tr.selectedRow() != 3 {
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
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	b.RecordEvent("ping", "Selected B event")
	type set struct{ targets []*stats.TargetStats }
	var source atomic.Pointer[set]
	source.Store(&set{[]*stats.TargetStats{a, b}})
	deleted := make(chan uint64, 1)
	h := startTUI(t, RunOptions{
		Targets:      []*stats.TargetStats{a, b},
		TargetSource: func() TargetSet { return TargetSet{Targets: source.Load().targets} },
		OnDeleteTarget: func(id uint64) error {
			deleted <- id
			source.Store(&set{[]*stats.TargetStats{a}})
			return nil
		},
	}, 140, 45)
	h.wait("same")
	h.key(tcell.KeyDown)
	h.key(tcell.KeyEnter)
	h.wait(fmt.Sprintf("Target #%d", b.ID()))
	h.key(tcell.KeyPgDn)
	h.wait("Selected B event")
	h.press('d')
	h.wait("このホストを削除しますか？")
	// Repeated d and Enter on the default Cancel must not delete the target.
	h.press('d')
	h.press('d')
	h.key(tcell.KeyEnter)
	h.wait("Selected B event")
	select {
	case <-deleted:
		t.Fatal("default cancellation deleted the target")
	default:
	}
	h.press('d')
	h.wait("このホストを削除しますか？")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	if id := recvWithin(t, deleted, "the delete callback"); id != b.ID() {
		t.Fatal("deleted wrong duplicate")
	}
	h.wait("Ping Monitor")
	h.quit()
}

func TestDetailsOptionalSectionsFollowOptionsEvenWithCachedData(t *testing.T) {
	target := stats.NewTargetStats("router")
	target.SetASNInfo("AS64500", "JP", "Example")
	target.SetPTR("router.example")
	target.SetPMTU(1400)
	target.SetTraceHops([]string{"192.0.2.1"})
	target.MTR().EnsureLen(1)
	target.SetPortResults([]*stats.PortCheckResult{stats.NewPortCheckResult(443, "tcp")})
	off := renderHostDetails(target, RunOptions{})
	on := renderHostDetails(target, RunOptions{ASNEnabled: true, PTREnabled: true, MTUEnabled: true, TraceEnabled: true, MTREnabled: true, PortEnabled: true})
	for _, label := range []string{"ASN:", "PTR:", "PMTU payload:", "[yellow]Route", "[yellow]MTR", "[yellow]Ports"} {
		if strings.Contains(off, label) {
			t.Fatalf("disabled option shown: %s", label)
		}
		if !strings.Contains(on, label) {
			t.Fatalf("enabled option hidden: %s", label)
		}
	}
	if detailTimestamp(time.Time{}) != "–" {
		t.Fatal("zero timestamp shown as a year-one date")
	}
}

// TestDetailsPageKeyRouting checks the details page's own keys: Tab moves
// between its panes, other keys stay with the page, the global R shortcut
// still reaches the reset callback, and Esc returns to the overview.
func TestDetailsPageKeyRouting(t *testing.T) {
	target := stats.NewTargetStats("router")
	resets := make(chan struct{}, 1)
	h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{target}, OnReset: func() { resets <- struct{}{} }}, 140, 45)
	h.wait("router")
	h.key(tcell.KeyEnter)
	h.wait(fmt.Sprintf("Target #%d", target.ID()))

	h.key(tcell.KeyTab) // to the graph
	h.key(tcell.KeyTab) // and back to the text
	h.press('x')        // not a details or global key: no effect
	h.press('R')
	recvWithin(t, resets, "the reset callback")
	h.wait(fmt.Sprintf("Target #%d", target.ID()))

	h.key(tcell.KeyEscape)
	h.waitWithout("Ping Monitor", fmt.Sprintf("Target #%d", target.ID()))
	h.quit()
}
