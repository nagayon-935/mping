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

// renderTracerouteTable builds the traceroute monitor table string.
func renderTracerouteTable(targets []*stats.TargetStats, availW int) string {
	hostColW := maxHostWidth("Host", targets)

	hopsColW := runewidth.StringWidth("Hops") + 2
	initTTLColW := runewidth.StringWidth("Init TTL") + 2

	fullRouteContentW := availW - hostColW - hopsColW - initTTLColW - 5
	traceCompact := fullRouteContentW < minRouteContentWidth

	dataTargets := make([]*stats.TargetStats, 0, len(targets))
	for _, t := range targets {
		if len(t.GetView().TraceHops) > 0 {
			dataTargets = append(dataTargets, t)
		}
	}

	var sb strings.Builder

	if traceCompact {
		routeColW := availW - hostColW - 3
		if routeColW < minRouteContentWidth {
			routeColW = minRouteContentWidth
		}
		routeContentW := routeColW - 1
		cols := []int{hostColW, routeColW}

		fmt.Fprintln(&sb, boxBorder(cols, borderTop))
		// Host is right-padded, Route center-padded — custom header (not boxHeaderRow).
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			rightPaddedCell("Host", hostColW), paddedCell("Route", routeColW))
		fmt.Fprintln(&sb, boxBorder(cols, borderMid))

		for i, t := range dataTargets {
			view := t.GetView()
			routeLines := wrapHops(view.TraceHops, routeContentW)
			if len(routeLines) == 0 {
				routeLines = []string{""}
			}
			midIdx := 0
			for j, rl := range routeLines {
				hostStr := ""
				if j == midIdx {
					hostStr = tview.Escape(view.Host)
				}
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					rightPaddedCell(hostStr, hostColW), paddedCell(rl, routeColW))
			}
			if i < len(dataTargets)-1 {
				fmt.Fprintln(&sb, boxBorder(cols, borderMid))
			}
		}
		fmt.Fprintln(&sb, boxBorder(cols, borderBottom))
	} else {
		routeColW := fullRouteContentW
		routeContentW := routeColW - 1
		cols := []int{hostColW, hopsColW, initTTLColW, routeColW}

		fmt.Fprintln(&sb, boxBorder(cols, borderTop))
		// Host/Hops/Init TTL right-padded, Route center-padded — custom header.
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			rightPaddedCell("Host", hostColW), rightPaddedCell("Hops", hopsColW), rightPaddedCell("Init TTL", initTTLColW), paddedCell("Route", routeColW))
		fmt.Fprintln(&sb, boxBorder(cols, borderMid))

		for i, t := range dataTargets {
			view := t.GetView()
			hopsStrVal := hopCountString(view.TraceHops)
			initTTLStrVal := inferInitialTTL(view.LastTTL)
			routeLines := wrapHops(view.TraceHops, routeContentW)
			if len(routeLines) == 0 {
				routeLines = []string{""}
			}
			midIdx := 0
			for j, rl := range routeLines {
				hostStr := ""
				hopsStr := ""
				initTTLStr := ""
				if j == midIdx {
					hostStr = tview.Escape(view.Host)
					hopsStr = hopsStrVal
					initTTLStr = initTTLStrVal
				}
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					rightPaddedCell(hostStr, hostColW), rightPaddedCell(hopsStr, hopsColW), rightPaddedCell(initTTLStr, initTTLColW), paddedCell(rl, routeColW))
			}
			if i < len(dataTargets)-1 {
				fmt.Fprintln(&sb, boxBorder(cols, borderMid))
			}
		}
		fmt.Fprintln(&sb, boxBorder(cols, borderBottom))
	}

	return sb.String()
}

func maxPortColumnWidth(header string, targets []*stats.TargetStats, extractor func(stats.PortCheckView) string) int {
	w := runewidth.StringWidth(header)
	for _, t := range targets {
		for _, pr := range t.GetView().PortResults {
			if cur := runewidth.StringWidth(extractor(pr)); cur > w {
				w = cur
			}
		}
	}
	return w + 2
}

// renderPortMonitorTable builds the port monitor table string.
// It also detects status changes and appends log messages.
func renderPortMonitorTable(targets []*stats.TargetStats, availW int, lastPortStatuses map[string]string, errorLogs *[]string, errorView *tview.TextView) string {
	targetColW := maxHostWidth("Target", targets)

	portColW := maxPortColumnWidth("Port", targets, func(pr stats.PortCheckView) string {
		return fmt.Sprintf("%d/%s", pr.Port, pr.Protocol)
	})

	serviceColW := maxPortColumnWidth("Service", targets, func(pr stats.PortCheckView) string {
		return portServiceName(pr.Port, pr.Protocol)
	})

	statusColW := runewidth.StringWidth("Open|Filtered") + 2

	lastColW := maxPortColumnWidth("Last", targets, func(pr stats.PortCheckView) string {
		return formatRTT(pr.RTT)
	})
	minColW := maxPortColumnWidth("Min", targets, func(pr stats.PortCheckView) string {
		return formatRTT(pr.MinRTT)
	})
	avgColW := maxPortColumnWidth("Avg", targets, func(pr stats.PortCheckView) string {
		return formatRTT(pr.AvgRTT)
	})
	maxColW := maxPortColumnWidth("Max", targets, func(pr stats.PortCheckView) string {
		return formatRTT(pr.MaxRTT)
	})

	countColW := runewidth.StringWidth("Open/Closed") + 2
	changeColW := runewidth.StringWidth("Last Change") + 2

	usedFull := targetColW + portColW + serviceColW + statusColW + lastColW + minColW + avgColW + maxColW + countColW + changeColW + 11
	portCompact := availW-targetColW-portColW-statusColW-lastColW-5 < minPortContentWidth

	// Collect targets that have results; detect status changes
	dataTargets := make([]*stats.TargetStats, 0, len(targets))
	for _, t := range targets {
		view := t.GetView()
		if len(view.PortResults) == 0 {
			continue
		}
		dataTargets = append(dataTargets, t)
		for _, pr := range view.PortResults {
			key := fmt.Sprintf("%s|%d/%s", view.Host, pr.Port, pr.Protocol)
			subject := fmt.Sprintf("[white]%s[-] [white]%d/%s:[white]", tview.Escape(view.Host), pr.Port, pr.Protocol)
			logStatusChangeIfNeeded(lastPortStatuses, key, pr.Status, "Open", subject, errorLogs, errorView)
		}
	}

	var sb strings.Builder

	if portCompact {
		cols := []int{targetColW, portColW, statusColW, lastColW}

		fmt.Fprintln(&sb, boxBorder(cols, borderTop))
		fmt.Fprintln(&sb, boxHeaderRow([]string{"Target", "Port", "Status", "Last"}, cols))
		fmt.Fprintln(&sb, boxBorder(cols, borderMid))

		rowCount := 0
		for ti, t := range dataTargets {
			view := t.GetView()
			for i, pr := range view.PortResults {
				targetName := ""
				if i == 0 {
					targetName = tview.Escape(view.Host)
				}
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│[-]\n",
					paddedCell(targetName, targetColW),
					paddedCell(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol), portColW),
					statusColorTag(pr.Status), paddedCell(pr.Status, statusColW),
					paddedCell(formatRTT(pr.RTT), lastColW))
				rowCount++
			}
			if ti < len(dataTargets)-1 {
				fmt.Fprintln(&sb, boxBorder(cols, borderMid))
			}
		}
		if rowCount == 0 {
			total := targetColW + portColW + statusColW + lastColW + 3
			fmt.Fprintln(&sb, boxSpanRow(" Waiting for results...", total, "[darkgray]"))
		}
		fmt.Fprintln(&sb, boxBorder(cols, borderBottom))
	} else {
		if availW > usedFull {
			changeColW += availW - usedFull
		}

		cols := []int{targetColW, portColW, serviceColW, statusColW, lastColW,
			minColW, avgColW, maxColW, countColW, changeColW}

		fmt.Fprintln(&sb, boxBorder(cols, borderTop))
		fmt.Fprintln(&sb, boxHeaderRow([]string{"Target", "Port", "Service", "Status",
			"Last", "Min", "Avg", "Max", "Open/Closed", "Last Change"}, cols))
		fmt.Fprintln(&sb, boxBorder(cols, borderMid))

		rowCount := 0
		for ti, t := range dataTargets {
			view := t.GetView()
			for i, pr := range view.PortResults {
				countStr := fmt.Sprintf("%d/%d", pr.OpenCount, pr.ClosedCount)
				changeStr := "-"
				if !pr.LastChange.IsZero() {
					changeStr = formatLossAgo(pr.LastChange)
				}
				targetName := ""
				if i == 0 {
					targetName = tview.Escape(view.Host)
				}
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					paddedCell(targetName, targetColW),
					paddedCell(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol), portColW),
					paddedCell(portServiceName(pr.Port, pr.Protocol), serviceColW),
					statusColorTag(pr.Status), paddedCell(pr.Status, statusColW),
					paddedCell(formatRTT(pr.RTT), lastColW),
					paddedCell(formatRTT(pr.MinRTT), minColW),
					paddedCell(formatRTT(pr.AvgRTT), avgColW),
					paddedCell(formatRTT(pr.MaxRTT), maxColW),
					paddedCell(countStr, countColW),
					paddedCell(changeStr, changeColW))
				rowCount++
			}
			if ti < len(dataTargets)-1 {
				fmt.Fprintln(&sb, boxBorder(cols, borderMid))
			}
		}
		if rowCount == 0 {
			total := targetColW + portColW + serviceColW + statusColW + lastColW + minColW + avgColW + maxColW + countColW + changeColW + 9
			fmt.Fprintln(&sb, boxSpanRow(" Waiting for results...", total, "[darkgray]"))
		}
		fmt.Fprintln(&sb, boxBorder(cols, borderBottom))
	}

	return sb.String()
}
