package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// newTestInputHandler builds a handler whose focus starts on the table.
// Callers adjust the returned deps before calling build.
func newTestInputHandler(t *testing.T) (*inputHandlerDeps, func() *inputHandler) {
	t.Helper()
	s := newUISession()
	t.Cleanup(func() { s.Stop(); s.Wait() })
	app := tview.NewApplication()
	table := tview.NewTable()
	app.SetFocus(table)
	rows := 1
	d := &inputHandlerDeps{
		app: app, table: table, addHostInput: tview.NewInputField(), deleteHostInput: tview.NewInputField(),
		footer: tview.NewTextView(), pages: tview.NewPages(),
		vs: newViewState(tview.NewTextView()), rowCount: &rows, session: s,
	}
	return d, func() *inputHandler { return &inputHandler{inputHandlerDeps: *d, state: monitorRunning} }
}

func press(h *inputHandler, key tcell.Key, r rune) *tcell.EventKey {
	return h.handle(tcell.NewEventKey(key, r, tcell.ModNone))
}

// runNextUpdate runs one UI update posted by a session callback, as tview's
// input loop would.
func runNextUpdate(t *testing.T, s *uiSession) {
	t.Helper()
	recvWithin(t, s.updates, "a UI update to be posted")()
}

func TestScrollKeysWithoutNavigationClampTheTableOffset(t *testing.T) {
	d, build := newTestInputHandler(t)
	rows := 3 * tableMaxRows
	d.rowCount = &rows
	redraws := 0
	d.forceUpdate = func() { redraws++ }
	h := build()
	maxOffset := rows - (tableMaxRows + 1)

	press(h, tcell.KeyUp, 0)
	if row, _ := h.table.GetOffset(); row != 0 {
		t.Fatalf("Up at the top moved the offset to %d", row)
	}
	press(h, tcell.KeyDown, 0)
	if row, _ := h.table.GetOffset(); row != 1 {
		t.Fatalf("Down offset = %d, want 1", row)
	}
	for range 5 {
		press(h, tcell.KeyPgDn, 0)
	}
	if row, _ := h.table.GetOffset(); row != maxOffset {
		t.Fatalf("PgDn offset = %d, want clamped to %d", row, maxOffset)
	}
	press(h, tcell.KeyPgUp, 0)
	if row, _ := h.table.GetOffset(); row != maxOffset-tableMaxRows {
		t.Fatalf("PgUp offset = %d, want %d", row, maxOffset-tableMaxRows)
	}
	if redraws != 8 {
		t.Fatalf("table redrawn %d times, want once per scroll key (8)", redraws)
	}
}

func TestDeleteKeyOpensTheHostPromptWithoutASelectionAction(t *testing.T) {
	d, build := newTestInputHandler(t)
	d.pages.AddPage("main", tview.NewBox(), true, true)
	d.pages.AddPage("deleteHost", d.deleteHostInput, true, false)
	d.onDeleteHost = func(string) error { return nil }
	h := build()

	if press(h, tcell.KeyRune, 'd') != nil {
		t.Fatal("d was not consumed")
	}

	if front, _ := h.pages.GetFrontPage(); front != "deleteHost" {
		t.Fatalf("front page = %q, want deleteHost", front)
	}
	if h.app.GetFocus() != h.deleteHostInput {
		t.Fatal("focus did not move to the host prompt")
	}
}

func TestStopWithAFullQueueStaysRunningAndTellsTheUser(t *testing.T) {
	d, build := newTestInputHandler(t)
	d.session.Stop() // a stopped session refuses work like a full queue
	h := build()

	press(h, tcell.KeyRune, 's')

	if h.state != monitorRunning {
		t.Fatalf("state = %d, want running after a refused stop", h.state)
	}
	if !strings.Contains(strings.Join(h.vs.errorLogs, "\n"), "Operation queue full") {
		t.Fatalf("log = %q, want a queue-full message", h.vs.errorLogs)
	}
}

func TestRestartFailureReturnsToStoppedAndLogsTheError(t *testing.T) {
	d, build := newTestInputHandler(t)
	d.onRestart = func() error { return errors.New("socket closed") }
	h := build()
	h.state = monitorStopped

	press(h, tcell.KeyRune, 'S')
	runNextUpdate(t, h.session)

	if h.state != monitorStopped {
		t.Fatalf("state = %d, want stopped", h.state)
	}
	if !strings.HasPrefix(h.footer.GetText(false), "Stopped.") {
		t.Fatalf("footer = %q, want the stopped hint", h.footer.GetText(false))
	}
	if !strings.Contains(strings.Join(h.vs.errorLogs, "\n"), "Restart failed: socket closed") {
		t.Fatalf("log = %q, want the restart error", h.vs.errorLogs)
	}
}

func TestRestartSuccessShowsTheRunningKeyHints(t *testing.T) {
	d, build := newTestInputHandler(t)
	d.onRestart = func() error { return nil }
	h := build()
	h.state = monitorStopped

	press(h, tcell.KeyRune, 'S')
	runNextUpdate(t, h.session)

	if h.state != monitorRunning {
		t.Fatalf("state = %d, want running", h.state)
	}
	if !strings.HasPrefix(h.footer.GetText(false), "Enter Detail") {
		t.Fatalf("footer = %q, want the running key hints", h.footer.GetText(false))
	}
}

func TestResetWithoutCallbackClearsStatsAndRestartsEnabledMonitors(t *testing.T) {
	d, build := newTestInputHandler(t)
	target := stats.NewTargetStats("example.com")
	target.IncSent()
	setRouteHops(target, "192.0.2.1")
	d.targets = []*stats.TargetStats{target}
	d.routeEnabled, d.portEnabled, d.httpEnabled = true, true, true
	calls := make(chan string, 3)
	d.onResetRoute = func() { calls <- "route" }
	d.onResetPort = func() { calls <- "port" }
	d.onResetHTTP = func() { calls <- "http" }
	h := build()

	press(h, tcell.KeyRune, 'R')

	var got []string
	for range 3 {
		got = append(got, recvWithin(t, calls, fmt.Sprintf("a monitor reset (got %v, want route, port and http)", got)))
	}
	if strings.Join(got, ",") != "route,port,http" {
		t.Fatalf("monitor resets = %v, want route,port,http", got)
	}
	if v := target.GetView(); v.Sent != 0 || len(v.MTRHops) != 0 {
		t.Fatalf("target after reset: sent=%d hops=%v, want 0 and no hops", v.Sent, v.MTRHops)
	}
}

func TestInputStateFollowsBrowserControlsWithoutOvertakingPendingOperations(t *testing.T) {
	actual := "stopped"
	h := &inputHandler{inputHandlerDeps: inputHandlerDeps{app: tview.NewApplication(), measurementState: func() string { return actual }}}
	h.handle(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if h.state != monitorStopped {
		t.Fatal("browser stop did not reach keyboard state")
	}
	actual = "running"
	h.handle(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if h.state != monitorRunning {
		t.Fatal("browser restart did not reach keyboard state")
	}
	h.state = monitorStopping
	h.handle(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if h.state != monitorStopping {
		t.Fatal("external state overtook queued stop")
	}
}
