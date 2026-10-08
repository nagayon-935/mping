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
		if cur := runewidth.StringWidth(t.GetView().Host); cur > w {
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

// boxColumn is one column of a box-drawn monitor table. Cells are padded
// to width (right-aligned unless center) and coloured white, or by tag when
// tag is set (status columns).
type boxColumn struct {
	header string
	width  int
	center bool
	tag    func(value string) string
}

func (c boxColumn) cell(value string) string {
	if c.center {
		return paddedCell(value, c.width)
	}
	return rightPaddedCell(value, c.width)
}

func boxWidths(cols []boxColumn) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = c.width
	}
	return widths
}

// writeBoxHeader writes the top border, bold yellow headers and the rule
// below them.
func writeBoxHeader(sb *strings.Builder, cols []boxColumn) {
	widths := boxWidths(cols)
	fmt.Fprintln(sb, boxBorder(widths, borderTop))
	sb.WriteString("[white]│")
	for _, c := range cols {
		sb.WriteString("[yellow::b]" + c.cell(c.header) + "[white]│")
	}
	sb.WriteString("[-]\n")
	fmt.Fprintln(sb, boxBorder(widths, borderMid))
}

func writeBoxRow(sb *strings.Builder, cols []boxColumn, values []string) {
	sb.WriteString("[white]│")
	for i, c := range cols {
		if c.tag != nil {
			sb.WriteString(c.tag(values[i]) + c.cell(values[i]) + "[-][white]│")
		} else {
			sb.WriteString("[white]" + c.cell(values[i]) + "[white]│")
		}
	}
	sb.WriteString("[-]\n")
}

// renderTracerouteTable builds the traceroute monitor table string. Narrow
// panes drop the Hops and Init TTL columns.
func renderTracerouteTable(targets []*stats.TargetStats, availW int) string {
	hostColW := maxHostWidth("Host", targets)
	hopsColW := runewidth.StringWidth("Hops") + 2
	initTTLColW := runewidth.StringWidth("Init TTL") + 2
	fullRouteContentW := availW - hostColW - hopsColW - initTTLColW - 5
	compact := fullRouteContentW < minRouteContentWidth

	cols := []boxColumn{{header: "Host", width: hostColW}}
	if compact {
		cols = append(cols, boxColumn{header: "Route", width: max(availW-hostColW-3, minRouteContentWidth), center: true})
	} else {
		cols = append(cols,
			boxColumn{header: "Hops", width: hopsColW},
			boxColumn{header: "Init TTL", width: initTTLColW},
			boxColumn{header: "Route", width: fullRouteContentW, center: true})
	}
	routeContentW := cols[len(cols)-1].width - 1

	var views []stats.TargetView
	for _, t := range targets {
		if view := t.GetView(); len(view.TraceHops) > 0 {
			views = append(views, view)
		}
	}

	var sb strings.Builder
	writeBoxHeader(&sb, cols)
	for i, view := range views {
		routeLines := wrapHops(view.TraceHops, routeContentW)
		if len(routeLines) == 0 {
			routeLines = []string{""}
		}
		// Host, hop count and initial TTL go on the first line only.
		first := []string{tview.Escape(view.Host)}
		if !compact {
			first = append(first, hopCountString(view.TraceHops), inferInitialTTL(view.LastTTL))
		}
		for j, line := range routeLines {
			values := make([]string, len(cols)-1, len(cols))
			if j == 0 {
				copy(values, first)
			}
			writeBoxRow(&sb, cols, append(values, line))
		}
		if i < len(views)-1 {
			fmt.Fprintln(&sb, boxBorder(boxWidths(cols), borderMid))
		}
	}
	fmt.Fprintln(&sb, boxBorder(boxWidths(cols), borderBottom))
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

// portColumn is a port monitor column with the value it shows per result.
type portColumn struct {
	boxColumn
	value func(stats.PortCheckView) string
}

func portLabel(pr stats.PortCheckView) string { return fmt.Sprintf("%d/%s", pr.Port, pr.Protocol) }

// renderPortMonitorTable builds the port monitor table string. Narrow panes
// show only Target, Port, Status and Last. It also detects status changes
// and appends log messages.
func renderPortMonitorTable(targets []*stats.TargetStats, availW int, lastPortStatuses map[string]string, errorLogs *[]string, errorView *tview.TextView) string {
	// Targets that have results; detect status changes.
	var views []stats.TargetView
	for _, t := range targets {
		view := t.GetView()
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

	column := func(header string, value func(stats.PortCheckView) string) portColumn {
		return portColumn{boxColumn{header: header, width: maxPortColumnWidth(header, views, value), center: true}, value}
	}
	rtt := func(get func(stats.PortCheckView) time.Duration) func(stats.PortCheckView) string {
		return func(pr stats.PortCheckView) string { return formatRTT(get(pr)) }
	}
	target := portColumn{boxColumn{header: "Target", width: maxHostWidth("Target", targets), center: true}, nil}
	port := column("Port", portLabel)
	service := column("Service", func(pr stats.PortCheckView) string { return portServiceName(pr.Port, pr.Protocol) })
	status := portColumn{boxColumn{header: "Status", width: runewidth.StringWidth("Open|Filtered") + 2, center: true, tag: statusColorTag},
		func(pr stats.PortCheckView) string { return pr.Status }}
	last := column("Last", rtt(func(pr stats.PortCheckView) time.Duration { return pr.RTT }))
	minC := column("Min", rtt(func(pr stats.PortCheckView) time.Duration { return pr.MinRTT }))
	avg := column("Avg", rtt(func(pr stats.PortCheckView) time.Duration { return pr.AvgRTT }))
	maxC := column("Max", rtt(func(pr stats.PortCheckView) time.Duration { return pr.MaxRTT }))
	count := portColumn{boxColumn{header: "Open/Closed", width: runewidth.StringWidth("Open/Closed") + 2, center: true},
		func(pr stats.PortCheckView) string { return fmt.Sprintf("%d/%d", pr.OpenCount, pr.ClosedCount) }}
	change := portColumn{boxColumn{header: "Last Change", width: runewidth.StringWidth("Last Change") + 2, center: true},
		func(pr stats.PortCheckView) string {
			if pr.LastChange.IsZero() {
				return "-"
			}
			return formatLossAgo(pr.LastChange)
		}}

	cols := []portColumn{target, port, status, last}
	if availW-target.width-port.width-status.width-last.width-5 >= minPortContentWidth {
		cols = []portColumn{target, port, service, status, last, minC, avg, maxC, count, change}
		// The last column takes any spare width.
		used := 10 + 1
		for _, c := range cols {
			used += c.width
		}
		if availW > used {
			cols[len(cols)-1].width += availW - used
		}
	}
	boxCols := make([]boxColumn, len(cols))
	for i, c := range cols {
		boxCols[i] = c.boxColumn
	}

	var sb strings.Builder
	writeBoxHeader(&sb, boxCols)
	for ti, view := range views {
		for i, pr := range view.PortResults {
			values := make([]string, len(cols))
			for ci, c := range cols {
				switch {
				case c.value != nil:
					values[ci] = c.value(pr)
				case i == 0: // the target name, on its first port only
					values[ci] = tview.Escape(view.Host)
				}
			}
			writeBoxRow(&sb, boxCols, values)
		}
		if ti < len(views)-1 {
			fmt.Fprintln(&sb, boxBorder(boxWidths(boxCols), borderMid))
		}
	}
	if len(views) == 0 {
		total := len(cols) - 1
		for _, c := range cols {
			total += c.width
		}
		fmt.Fprintln(&sb, boxSpanRow(" Waiting for results...", total, "[darkgray]"))
	}
	fmt.Fprintln(&sb, boxBorder(boxWidths(boxCols), borderBottom))
	return sb.String()
}
