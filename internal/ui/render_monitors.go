package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

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
		time.Now().Format("15:04:05"), subject, prevStatus, color, newStatus)
}

// logStatusChangeIfNeeded checks lastStatuses[key] against newStatus and, if
// it changed, appends a log line built by statusChangeMessage. It always
// records newStatus under key afterward (a no-op for the initial ""/
// "Checking..." placeholder status). Shared by the port and HTTP monitor
// panes, whose status-change detection was previously duplicated verbatim
// (TD-45).
func logStatusChangeIfNeeded(lastStatuses map[string]string, key, newStatus, healthyStatus, subject string,
	errorLogs *[]string, errorView *tview.TextView) {
	if newStatus == "" || newStatus == "Checking..." {
		return
	}
	if prev, seen := lastStatuses[key]; seen && prev != newStatus && errorView != nil {
		appendErrorLog(errorLogs, errorView, statusChangeMessage(subject, prev, newStatus, healthyStatus))
	}
	lastStatuses[key] = newStatus
}

// renderTracerouteTable builds the traceroute monitor table string. Narrow
// panes drop the Hops and Init TTL columns.
func renderTracerouteTable(targets []*stats.TargetStats, availW int) string {
	hostColW := maxHostWidth("Host", targets)
	hopsColW := runewidth.StringWidth("Hops") + 2
	initTTLColW := runewidth.StringWidth("Init TTL") + 2
	fullRouteContentW := availW - hostColW - hopsColW - initTTLColW - 5
	compact := fullRouteContentW < minRouteContentWidth

	cols := []boxColumn{{header: "Host", width: hostColW, leftAlign: true}}
	if compact {
		cols = append(cols, boxColumn{header: "Route", width: max(availW-hostColW-3, minRouteContentWidth)})
	} else {
		cols = append(cols,
			boxColumn{header: "Hops", width: hopsColW, leftAlign: true},
			boxColumn{header: "Init TTL", width: initTTLColW, leftAlign: true},
			boxColumn{header: "Route", width: fullRouteContentW})
	}
	routeContentW := cols[len(cols)-1].width - 1

	var groups [][][]boxCell
	for _, t := range targets {
		view := t.GetViewWindow(0)
		if len(view.TraceHops) == 0 {
			continue
		}
		routeLines := wrapHops(view.TraceHops, routeContentW)
		if len(routeLines) == 0 {
			routeLines = []string{""}
		}
		// Host, hop count and initial TTL go on the first line only.
		first := []string{tview.Escape(view.Host)}
		if !compact {
			first = append(first, hopCountString(view.TraceHops), inferInitialTTL(view.LastTTL))
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
func renderPortMonitorTable(targets []*stats.TargetStats, availW int, lastPortStatuses map[string]string, errorLogs *[]string, errorView *tview.TextView) string {
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
			subject := fmt.Sprintf("[white]%s[-] [white]%d/%s:[white]", tview.Escape(view.Host), pr.Port, pr.Protocol)
			logStatusChangeIfNeeded(lastPortStatuses, key, pr.Status, "Open", subject, errorLogs, errorView)
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
		return func(pr stats.PortCheckView) string { return formatRTT(get(pr)) }
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
	last := sized("Last", rtt(func(pr stats.PortCheckView) time.Duration { return pr.RTT }), false)
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
