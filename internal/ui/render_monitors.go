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

// logStatusChangeIfNeeded checks lastStatuses[key] against newStatus and, if
// it changed, appends a log line. It always
// records newStatus under key afterward (a no-op for the initial ""/
// "Checking..." placeholder status). subject builds the line's subject and
// runs only when a change is logged, keeping formatting off the per-tick
// path. Shared by the port and HTTP monitor panes.
func logStatusChangeIfNeeded(lastStatuses map[string]string, key, newStatus string, colorTag func(string) string,
	subject func() string, log func(string)) {
	if lastStatuses == nil || newStatus == "" || newStatus == "Checking..." {
		return
	}
	if prev, seen := lastStatuses[key]; seen && prev != newStatus && log != nil {
		log(fmt.Sprintf("[darkgray]%s[-] %s %s → %s%s[-]",
			time.Now().Format(logTimeLayout), subject(), prev, colorTag(newStatus), newStatus))
	}
	lastStatuses[key] = newStatus
}

// renderTracerouteTable builds the route pane in path mode (-T): one row per
// target with its discovered hops. Narrow panes drop the Hops and Init TTL
// columns.
func renderTracerouteTable(targets []*stats.TargetStats, availW int) string {
	hostColW := maxHostWidth("Host", targets)
	hopsColW := len("Hops") + 2
	initTTLColW := len("Init TTL") + 2
	fullRouteW := availW - hostColW - hopsColW - initTTLColW - 5
	compact := fullRouteW < minRouteContentWidth+2

	cols := []boxColumn{{header: "Host", width: hostColW}}
	routeW := fullRouteW
	if compact {
		const minRouteW = len("Route") + 2
		hostColW = min(hostColW, availW-3-minRouteW)
		if hostColW < 2 {
			return truncateToDisplayWidth(monitorTooNarrow, availW)
		}
		cols[0].width = hostColW
		routeW = availW - hostColW - 3
	} else {
		cols = append(cols,
			boxColumn{header: "Hops", width: hopsColW, rightAlign: true},
			boxColumn{header: "Init TTL", width: initTTLColW, rightAlign: true})
	}
	routeContentW := routeW - 2
	widestRoute := len("Route")

	var groups [][][]boxCell
	for _, t := range targets {
		view := t.GetViewWindow(0)
		route, hopCount := hopLabels(view.MTRHops), fmt.Sprint(len(view.MTRHops))
		if len(route) == 0 {
			hopCount = "-"
			if view.MTRError != "" {
				route = []string{"Route discovery failed: " + view.MTRError}
			} else {
				route = []string{"Discovering..."}
			}
		}
		routeLines := wrapHops(route, routeContentW)
		if view.MTRFlapCount > 0 {
			routeLines = append(routeLines, fmt.Sprintf("[FLAP %d times %s]", view.MTRFlapCount, view.MTRLastFlapAt.Format(logTimeLayout)))
		}
		// Host, hop count and initial TTL go on the first line only.
		first := []string{view.Host}
		if !compact {
			first = append(first, hopCount, inferInitialTTL(view.LastTTL))
		}
		rows := make([][]boxCell, len(routeLines))
		for i, line := range routeLines {
			widestRoute = max(widestRoute, runewidth.StringWidth(line))
			row := make([]boxCell, len(cols)+1)
			if i == 0 {
				copy(row, plainCells(first...))
			}
			row[len(cols)] = boxCell{text: line}
			rows[i] = row
		}
		groups = append(groups, rows)
	}

	cols = append(cols, boxColumn{header: "Route", width: min(widestRoute, routeContentW) + 2})
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
			logStatusChangeIfNeeded(lastPortStatuses, key, pr.Status, statusColorTag, subject, log)
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
	// Latency and count columns have fixed widths so the table does not
	// shift as values change, like the other monitors.
	latency := func(header string, text func(stats.PortCheckView) string, full bool) monitorColumn[portRow] {
		c := col(header, fixedMsColW, text, full)
		c.rightAlign = true
		return c
	}
	rtt := func(get func(stats.PortCheckView) time.Duration) func(stats.PortCheckView) string {
		return func(pr stats.PortCheckView) string {
			if pr.Status != "Open" {
				return "-"
			}
			return formatFixedMs(get(pr))
		}
	}
	// The target name is shown on the target's first port only.
	// Its minimum fits host names up to maxPortTargetW, so the full layout is
	// not picked at the cost of cutting short names that compact would show.
	target := monitorColumn[portRow]{boxColumn: boxColumn{header: "Target", width: min(maxHostWidth("Target", targets), maxPortTargetW)},
		cell: func(r portRow) boxCell {
			if r.first {
				return boxCell{text: r.host}
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
			return formatFixedMs(pr.RTT)
		case "Filtered", "Open|Filtered":
			if pr.RTT > 0 {
				return "timeout"
			}
		}
		return "-"
	}
	last := latency("Last", lastText, false)
	last.cell = func(r portRow) boxCell {
		text := lastText(r.pr)
		if text == "timeout" {
			return boxCell{text: text, tag: levelTag(stats.LevelNone)}
		}
		return boxCell{text: text}
	}
	minC := latency("Min", rtt(func(pr stats.PortCheckView) time.Duration { return pr.MinRTT }), true)
	avg := latency("Avg", rtt(func(pr stats.PortCheckView) time.Duration { return pr.AvgRTT }), true)
	maxC := latency("Max", rtt(func(pr stats.PortCheckView) time.Duration { return pr.MaxRTT }), true)
	count := col("Open/Closed", runewidth.StringWidth("Open/Closed")+2, func(pr stats.PortCheckView) string {
		return fmt.Sprintf("%d/%d", pr.OpenCount, pr.ClosedCount)
	}, true)
	count.rightAlign = true
	change := col("Last Change", timeAgoColW, func(pr stats.PortCheckView) string {
		return formatLossAgo(pr.LastChange)
	}, true)

	all := []monitorColumn[portRow]{target, port, service, status, last, minC, avg, maxC, count, change}
	cols, boxCols := fitMonitorColumns(all, "Target", availW)
	if len(cols) == 0 {
		return truncateToDisplayWidth(monitorTooNarrow, availW)
	}

	groups := make([][][]boxCell, len(views))
	for ti, view := range views {
		for i, pr := range view.PortResults {
			groups[ti] = append(groups[ti], rowCells(cols, portRow{host: view.Host, pr: pr, first: i == 0}))
		}
	}

	var sb strings.Builder
	writeBoxTable(&sb, boxCols, groups, "Waiting for results...")
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
