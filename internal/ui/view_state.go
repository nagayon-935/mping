package ui

import (
	"fmt"
	"regexp"
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

// healthLogLine matches a health transition as the supervisor logs it:
// "[15:04:05] host: crit: Jitter 43.3ms > 10.0ms".
var healthLogLine = regexp.MustCompile(`^\[(\d\d:\d\d:\d\d)\] (.+?): (ok|warn|crit|pending): (.+)$`)

// measuredValue matches one threshold check's measured value ("Jitter 43.3ms
// >"), which differs per host while the cause is the same.
var measuredValue = regexp.MustCompile(`([A-Za-z][A-Za-z ]*?) -?[0-9.]+(?:ms|%) >`)

// logGroupMaxHosts caps the hosts named in one grouped line.
const logGroupMaxHosts = 4

// healthLogGroup is a run of consecutive health transitions logged in the
// same second with the same status and cause, shown as one Log line.
type healthLogGroup struct {
	key    string // time, status and cause
	at     string
	status string
	cause  string
	first  string // the first line as logged, kept while the group has one host
	hosts  []string
}

// parseHealthLogLine returns the group a health transition line starts, or
// false for any other line.
func parseHealthLogLine(line string) (healthLogGroup, bool) {
	m := healthLogLine.FindStringSubmatch(line)
	if m == nil {
		return healthLogGroup{}, false
	}
	cause := measuredValue.ReplaceAllString(m[4], "$1 >")
	return healthLogGroup{
		key: m[1] + "|" + m[3] + "|" + cause, at: m[1], status: m[3], cause: cause,
		first: line, hosts: []string{m[2]},
	}, true
}

// line renders the group: the original line for one host, otherwise the
// shared cause once with the hosts it hit.
func (g healthLogGroup) line() string {
	if len(g.hosts) == 1 {
		return g.first
	}
	hosts := g.hosts
	more := ""
	if len(hosts) > logGroupMaxHosts {
		more = fmt.Sprintf(" +%d", len(hosts)-logGroupMaxHosts)
		hosts = hosts[:logGroupMaxHosts]
	}
	return fmt.Sprintf("[%s] %s: %s (%d hosts) — %s%s", g.at, g.status, g.cause, len(g.hosts), strings.Join(hosts, ", "), more)
}
