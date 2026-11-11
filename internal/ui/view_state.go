package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

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
	errorView *tview.TextView
	errorLogs []string
	// logGroup is the health transition group the last Log line shows, so
	// the same cause hitting several hosts in one second becomes one line.
	logGroup         *healthLogGroup
	lastLossTimes    map[string]time.Time
	alertState       map[string]alertFlags
	lastPortStatuses map[string]string
	lastHTTPStatuses map[string]string
	// thresholds are the run's current thresholds, for the HTTP checks,
	// which belong to no target (targets carry their own in TargetView).
	thresholds stats.Thresholds
}

func newViewState(errorView *tview.TextView) *viewState {
	return &viewState{
		errorView:        errorView,
		thresholds:       stats.DefaultThresholds(),
		lastLossTimes:    make(map[string]time.Time),
		alertState:       make(map[string]alertFlags),
		lastPortStatuses: make(map[string]string),
		lastHTTPStatuses: make(map[string]string),
	}
}

// appendLog appends msg to errorLogs and errorView, evicting the oldest
// line once errorLogMaxSize is exceeded (see appendErrorLog).
//
// A health transition with the same time, status and cause as the group the
// last line shows is folded into that line instead.
func (vs *viewState) appendLog(msg string) {
	g, ok := parseHealthLogLine(msg)
	if ok && vs.logGroup != nil && vs.logGroup.key == g.key && len(vs.errorLogs) > 0 {
		for _, h := range g.hosts {
			if !slices.Contains(vs.logGroup.hosts, h) {
				vs.logGroup.hosts = append(vs.logGroup.hosts, h)
			}
		}
		vs.errorLogs[len(vs.errorLogs)-1] = vs.logGroup.line()
		vs.errorView.SetText(strings.Join(vs.errorLogs, "\n") + "\n")
		vs.errorView.ScrollToEnd()
		return
	}
	vs.logGroup = nil
	if ok {
		vs.logGroup = &g
	}
	appendErrorLog(&vs.errorLogs, vs.errorView, msg)
}

// reset clears the log and the status caches — the 'R' key handler's
// log/state wipe. The thresholds are configuration and stay.
func (vs *viewState) reset() {
	vs.errorLogs = []string{}
	vs.logGroup = nil
	vs.errorView.SetText("")
	vs.lastLossTimes = make(map[string]time.Time)
	vs.alertState = make(map[string]alertFlags)
	vs.lastPortStatuses = make(map[string]string)
	vs.lastHTTPStatuses = make(map[string]string)
}
