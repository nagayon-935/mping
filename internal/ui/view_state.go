package ui

import (
	"time"

	"github.com/rivo/tview"
)

// viewState bundles the render-state containers shared by the table
// renderer, the key handler (inputHandlerDeps) and the port/HTTP monitor
// pane render closures. All three hold the same *viewState so they observe
// each other's reassignments.
//
// Concurrency invariant: none of viewState's fields are guarded by a mutex.
// This is safe only because every mutation happens on tview's single
// draw/event-loop goroutine — the goroutine that runs app.Run(), on which
// uiSession mailbox callbacks and the SetInputCapture key handler are
// dispatched serially. No other goroutine may read or write through a
// *viewState; if a future change needs to touch it from elsewhere (e.g. a
// background checker), it must post through uiSession first.
type viewState struct {
	errorView        *tview.TextView
	errorLogs        []string
	lastLossTimes    map[string]time.Time
	alertState       map[string]alertFlags
	lastPortStatuses map[string]string
	lastHTTPStatuses map[string]string
}

func newViewState(errorView *tview.TextView) *viewState {
	return &viewState{
		errorView:        errorView,
		lastLossTimes:    make(map[string]time.Time),
		alertState:       make(map[string]alertFlags),
		lastPortStatuses: make(map[string]string),
		lastHTTPStatuses: make(map[string]string),
	}
}

// appendLog appends msg to errorLogs and errorView, evicting the oldest
// line once errorLogMaxSize is exceeded (see appendErrorLog).
func (vs *viewState) appendLog(msg string) {
	appendErrorLog(&vs.errorLogs, vs.errorView, msg)
}

// reset clears all five containers — the 'R' key handler's log/state wipe.
func (vs *viewState) reset() {
	vs.errorLogs = []string{}
	vs.errorView.SetText("")
	vs.lastLossTimes = make(map[string]time.Time)
	vs.alertState = make(map[string]alertFlags)
	vs.lastPortStatuses = make(map[string]string)
	vs.lastHTTPStatuses = make(map[string]string)
}
