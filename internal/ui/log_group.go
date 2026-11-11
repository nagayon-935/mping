package ui

import (
	"fmt"
	"regexp"
	"strings"
)

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
	return fmt.Sprintf("[%s] %s: %s ×%d — %s%s", g.at, g.status, g.cause, len(g.hosts), strings.Join(hosts, ", "), more)
}
