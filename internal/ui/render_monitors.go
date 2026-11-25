package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// maxHostWidth returns the widest target display name or header, plus padding.
func maxHostWidth(header string, targets []*stats.TargetStats) int {
	w := runewidth.StringWidth(header)
	for _, t := range targets {
		if cur := runewidth.StringWidth(t.DisplayName()); cur > w {
			w = cur
		}
	}
	return w + 2
}

// statusChangeMessage formats a "prev -> new" status-change log line. subject
// is the pre-formatted, already color-tagged text identifying what changed
// (e.g. "[white]host[-] [white]443/tcp:[white]"). newStatus is colored green
// when it equals healthyStatus, yellow otherwise.
func statusChangeMessage(subject, prevStatus, newStatus, healthyStatus string) string {
	color := "[yellow]"
	if newStatus == healthyStatus {
		color = "[green]"
	}
	return fmt.Sprintf("[darkgray]%s[-] %s %s → %s%s[-]",
		time.Now().Format(logTimeLayout), subject, prevStatus, color, newStatus)
}

// logStatusChangeIfNeeded checks lastStatuses[key] against newStatus and, if
// it changed, appends a log line built by statusChangeMessage. It always
// records newStatus under key afterward (a no-op for the initial ""/
// "Checking..." placeholder status). subject builds the line's subject and
// runs only when a change is logged, keeping formatting off the per-tick
// path. Shared by the port and HTTP monitor panes.
func logStatusChangeIfNeeded(lastStatuses map[string]string, key, newStatus, healthyStatus string,
	subject func() string, log func(string)) {
	if newStatus == "" || newStatus == "Checking..." {
		return
	}
	if prev, seen := lastStatuses[key]; seen && prev != newStatus && log != nil {
		log(statusChangeMessage(subject(), prev, newStatus, healthyStatus))
	}
	lastStatuses[key] = newStatus
}

// renderTracerouteTable builds the route pane in path mode (-T): one row per
// target with its discovered hops. Narrow panes drop the Hops and Init TTL
// columns.
func renderTracerouteTable(targets []*stats.TargetStats, availW int) string {
	hostColW := maxHostWidth("Host", targets)
	hopsColW := runewidth.StringWidth("Hops") + 2
	initTTLColW := runewidth.StringWidth("Init TTL") + 2
	fullRouteContentW := availW - hostColW - hopsColW - initTTLColW - 5
	compact := fullRouteContentW < minRouteContentWidth

	cols := []boxColumn{{header: "Host", width: hostColW, rightAlign: true}}
	if compact {
		cols = append(cols, boxColumn{header: "Route", width: max(availW-hostColW-3, minRouteContentWidth)})
	} else {
		cols = append(cols,
			boxColumn{header: "Hops", width: hopsColW, rightAlign: true},
			boxColumn{header: "Init TTL", width: initTTLColW, rightAlign: true},
			boxColumn{header: "Route", width: fullRouteContentW})
	}
	routeContentW := cols[len(cols)-1].width - 1

	var groups [][][]boxCell
	for _, t := range targets {
		view := t.GetViewWindow(0)
		route, hopCount := hopLabels(view.MTRHops), fmt.Sprint(len(view.MTRHops))
		switch {
		case len(route) > 0:
		case view.MTRError != "":
			route, hopCount = []string{"Route discovery failed: " + view.MTRError}, "-"
		default:
			route, hopCount = []string{"Discovering..."}, "-"
		}
		routeLines := wrapHops(route, routeContentW)
		if len(routeLines) == 0 {
			routeLines = []string{""}
		}
		for j := range routeLines {
			routeLines[j] = tview.Escape(routeLines[j])
		}
		if view.MTRFlapCount > 0 {
			routeLines = append(routeLines, fmt.Sprintf("[FLAP %d times %s]", view.MTRFlapCount, view.MTRLastFlapAt.Format(logTimeLayout)))
		}
		// Host, hop count and initial TTL go on the first line only.
		first := []string{tview.Escape(view.Host)}
		if !compact {
			first = append(first, hopCount, inferInitialTTL(view.LastTTL))
		}
		rows := make([][]boxCell, len(routeLines))
		for j, line := range routeLines {
			texts := make([]string, len(cols)-1, len(cols))
			if j == 0 {
				copy(texts, first)
			}
			rows[j] = plainCells(append(texts, line)...)
		}
		groups = append(groups, rows)
	}

	var sb strings.Builder
	writeBoxTable(&sb, cols, groups, "")
	return sb.String()
}

// maxPortColumnWidth returns the widest extracted port value or header, plus padding.
func maxPortColumnWidth(header string, views []stats.TargetView, extractor func(stats.PortCheckView) string) int {
	w := runewidth.StringWidth(header)
	for _, view := range views {
		for _, pr := range view.PortResults {
			if cur := runewidth.StringWidth(extractor(pr)); cur > w {
				w = cur
			}
		}
	}
	return w + 2
}

// portRow is one port result of a target. first marks the target's first
// row, which is the only one that names the target.
type portRow struct {
	host  string
	pr    stats.PortCheckView
	first bool
}

func portLabel(pr stats.PortCheckView) string { return fmt.Sprintf("%d/%s", pr.Port, pr.Protocol) }

// renderPortMonitorTable builds the port monitor table string. Narrow panes
// show only Target, Port, Status and Last. It also detects status changes
// and appends log messages.
func renderPortMonitorTable(targets []*stats.TargetStats, availW int, lastPortStatuses map[string]string, log func(string)) string {
	// Targets that have results; detect status changes.
	var views []stats.TargetView
	for _, t := range targets {
		view := t.GetViewWindow(0)
		if len(view.PortResults) == 0 {
			continue
		}
		views = append(views, view)
		for _, pr := range view.PortResults {
			key := fmt.Sprintf("%s|%d/%s", targetViewKey(view), pr.Port, pr.Protocol)
			subject := func() string {
				return fmt.Sprintf("[white]%s[-] [white]%d/%s:[white]", tview.Escape(view.Host), pr.Port, pr.Protocol)
			}
			logStatusChangeIfNeeded(lastPortStatuses, key, pr.Status, "Open", subject, log)
		}
	}

	// col is a plain column of the given width showing text per port result.
	col := func(header string, width int, text func(stats.PortCheckView) string, full bool) monitorColumn[portRow] {
		return monitorColumn[portRow]{boxColumn{header: header, width: width},
			plainCell(func(r portRow) string { return text(r.pr) }), full}
	}
	// sized fits a plain column to its widest value.
	sized := func(header string, text func(stats.PortCheckView) string, full bool) monitorColumn[portRow] {
		return col(header, maxPortColumnWidth(header, views, text), text, full)
	}
	rtt := func(get func(stats.PortCheckView) time.Duration) func(stats.PortCheckView) string {
		return func(pr stats.PortCheckView) string {
			if pr.Status != "Open" {
				return "–"
			}
			return formatRTT(get(pr))
		}
	}
	fixed := func(header, widest string, text func(stats.PortCheckView) string, full bool) monitorColumn[portRow] {
		return col(header, runewidth.StringWidth(widest)+2, text, full)
	}
	// The target name is shown on the target's first port only.
	target := monitorColumn[portRow]{boxColumn: boxColumn{header: "Target", width: maxHostWidth("Target", targets)},
		cell: func(r portRow) boxCell {
			if r.first {
				return boxCell{text: tview.Escape(r.host)}
			}
			return boxCell{}
		}}
	port := sized("Port", portLabel, false)
	service := sized("Service", func(pr stats.PortCheckView) string { return portServiceName(pr.Port, pr.Protocol) }, true)
	status := monitorColumn[portRow]{boxColumn{header: "Status", width: runewidth.StringWidth("Open|Filtered") + 2},
		func(r portRow) boxCell { return boxCell{text: r.pr.Status, tag: statusColorTag(r.pr.Status)} }, false}
	lastText := func(pr stats.PortCheckView) string {
		switch pr.Status {
		case "Open":
			return formatRTT(pr.RTT)
		case "Filtered", "Open|Filtered":
			if pr.RTT > 0 {
				return "timeout"
			}
		}
		return "–"
	}
	last := sized("Last", lastText, false)
	last.cell = func(r portRow) boxCell {
		text := lastText(r.pr)
		if text == "timeout" {
			return boxCell{text: text, tag: "[gray]"}
		}
		return boxCell{text: text}
	}
	minC := sized("Min", rtt(func(pr stats.PortCheckView) time.Duration { return pr.MinRTT }), true)
	avg := sized("Avg", rtt(func(pr stats.PortCheckView) time.Duration { return pr.AvgRTT }), true)
	maxC := sized("Max", rtt(func(pr stats.PortCheckView) time.Duration { return pr.MaxRTT }), true)
	count := fixed("Open/Closed", "Open/Closed", func(pr stats.PortCheckView) string {
		return fmt.Sprintf("%d/%d", pr.OpenCount, pr.ClosedCount)
	}, true)
	change := fixed("Last Change", "Last Change", func(pr stats.PortCheckView) string {
		if pr.LastChange.IsZero() {
			return "-"
		}
		return formatLossAgo(pr.LastChange)
	}, true)

	// The extra columns need room beyond the four that always show.
	compact := availW-target.width-port.width-status.width-last.width-5 < minPortContentWidth
	all := []monitorColumn[portRow]{target, port, service, status, last, minC, avg, maxC, count, change}
	cols, boxCols := layoutColumns(all, compact)
	if !compact {
		// The last column takes any spare width.
		if spare := availW - (boxInnerWidth(boxCols) + 2); spare > 0 {
			boxCols[len(boxCols)-1].width += spare
		}
	}

	groups := make([][][]boxCell, len(views))
	for ti, view := range views {
		for i, pr := range view.PortResults {
			groups[ti] = append(groups[ti], rowCells(cols, portRow{host: view.Host, pr: pr, first: i == 0}))
		}
	}

	var sb strings.Builder
	writeBoxTable(&sb, boxCols, groups, " Waiting for results...")
	return sb.String()
}

// wellKnownServices maps a port number to its protocol -> service name.
var wellKnownServices = map[int]map[string]string{
	20:    {"tcp": "FTP-Data"},
	21:    {"tcp": "FTP"},
	22:    {"tcp": "SSH"},
	23:    {"tcp": "Telnet"},
	25:    {"tcp": "SMTP"},
	53:    {"tcp": "DNS", "udp": "DNS"},
	67:    {"udp": "DHCP"},
	68:    {"udp": "DHCP"},
	80:    {"tcp": "HTTP"},
	110:   {"tcp": "POP3"},
	123:   {"udp": "NTP"},
	143:   {"tcp": "IMAP"},
	161:   {"udp": "SNMP"},
	389:   {"tcp": "LDAP"},
	443:   {"tcp": "HTTPS"},
	445:   {"tcp": "SMB"},
	465:   {"tcp": "SMTPS"},
	514:   {"udp": "Syslog"},
	587:   {"tcp": "SMTP"},
	636:   {"tcp": "LDAPS"},
	993:   {"tcp": "IMAPS"},
	995:   {"tcp": "POP3S"},
	1433:  {"tcp": "MSSQL"},
	1521:  {"tcp": "Oracle"},
	2181:  {"tcp": "ZooKeeper"},
	3306:  {"tcp": "MySQL"},
	3389:  {"tcp": "RDP"},
	5432:  {"tcp": "PostgreSQL"},
	5672:  {"tcp": "AMQP"},
	5900:  {"tcp": "VNC"},
	6379:  {"tcp": "Redis"},
	8080:  {"tcp": "HTTP-Alt"},
	8443:  {"tcp": "HTTPS-Alt"},
	9200:  {"tcp": "Elasticsearch"},
	9300:  {"tcp": "Elasticsearch"},
	11211: {"tcp": "Memcached", "udp": "Memcached"},
	27017: {"tcp": "MongoDB"},
}

// portServiceName returns the well-known service name for port/protocol, or
// "Unknown" when there's no match.
func portServiceName(port int, protocol string) string {
	if protos, ok := wellKnownServices[port]; ok {
		if name, ok := protos[protocol]; ok {
			return name
		}
	}
	return "Unknown"
}
