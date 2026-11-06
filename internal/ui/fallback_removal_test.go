package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestResetWithoutCallbackOnlyClearsViewState(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.IncSent()
	setRouteHops(target, "192.0.2.1")
	before := target.GetView()
	tui := newTUI(RunOptions{Targets: []*stats.TargetStats{target}})
	tui.session = newUISession()
	defer func() { tui.session.Stop(); tui.session.Wait() }()
	tui.vs.appendLog("old log")
	tui.vs.lastPortStatuses["example.com:80"] = "Open"
	h := &inputHandler{inputHandlerDeps: tui.inputDeps(), state: monitorRunning}

	h.reset()
	finished := make(chan struct{})
	tui.session.Submit(func() { close(finished) })
	awaitSessionSignal(t, finished) // any reset work would run before this barrier

	if got := target.GetView(); got.Sent != before.Sent || len(got.MTRHops) != len(before.MTRHops) {
		t.Fatalf("reset without OnReset changed measurement statistics: sent=%d hops=%d", got.Sent, len(got.MTRHops))
	}
	if len(tui.vs.errorLogs) != 0 || tui.vs.errorView.GetText(false) != "" || len(tui.vs.lastPortStatuses) != 0 {
		t.Fatal("reset did not clear the view's log and status cache")
	}
}

func TestDeleteWithoutTargetCallbackKeepsTableFocus(t *testing.T) {
	tui := newTUI(RunOptions{Targets: []*stats.TargetStats{stats.NewTargetStats("example.com")}})
	tui.app.SetFocus(tui.table)
	h := &inputHandler{inputHandlerDeps: tui.inputDeps()}

	press(h, tcell.KeyRune, 'd')

	if page, _ := tui.pages.GetFrontPage(); page != "footer" || tui.app.GetFocus() != tui.table {
		t.Fatalf("delete without OnDeleteTarget opened a host prompt: page=%q", page)
	}
}

func TestScrollWithoutNavigationKeepsTableOffset(t *testing.T) {
	tui := newTUI(RunOptions{})
	tui.table.SetOffset(4, 2)
	tui.app.SetFocus(tui.table)
	deps := tui.inputDeps()
	deps.navigate = nil
	h := &inputHandler{inputHandlerDeps: deps}

	for _, key := range []tcell.Key{tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn} {
		press(h, key, 0)
		if row, col := tui.table.GetOffset(); row != 4 || col != 2 {
			t.Fatalf("key %v without navigation changed offset to (%d, %d), want (4, 2)", key, row, col)
		}
	}
}
