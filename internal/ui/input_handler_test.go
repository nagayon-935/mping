package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
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
	d := &inputHandlerDeps{
		app: app, table: table, addHostInput: tview.NewInputField(),
		footer: tview.NewTextView(), pages: tview.NewPages(),
		vs: newViewState(tview.NewTextView()), session: s,
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
