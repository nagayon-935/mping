package ui

import (
	"time"

	"github.com/rivo/tview"
)

// viewState bundles the five render-state containers that Run() previously
// declared as separate local variables and distributed as individual
// pointers to three destinations: newTableRenderer (ballooning its
// parameter list), inputHandlerDeps (five more individual pointer fields),
// and the port/HTTP monitor pane render closures. A single *viewState now
// flows to all three instead (TD-51).
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
