package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	rttOrangeThreshold    = 50 * time.Millisecond
	rttRedThreshold       = 200 * time.Millisecond
	jitterOrangeThreshold = 10 * time.Millisecond
	jitterRedThreshold    = 50 * time.Millisecond
	lossOrangeThreshold   = 20.0
	lossRedThreshold      = 80.0
	errorLogMaxSize       = 1000 // maximum number of lines kept in the error log pane
)

var tableErrorCandidates = []string{
	"DNS Error",
	"No Conn",
	"No IPv4 Conn",
	"No IPv6 Conn",
	"Timeout",
	"ICMP Error",
	"Destination Network Unreachable",
	"Destination Host Unreachable",
	"Destination Protocol Unreachable",
	"Destination Port Unreachable",
	"Fragmentation Needed",
	"Source Route Failed",
	"Destination Network Unknown",
	"Destination Host Unknown",
	"Source Host Isolated",
	"Network Administratively Prohibited",
	"Host Administratively Prohibited",
	"Network Unreachable for ToS",
	"Host Unreachable for ToS",
	"Communication Administratively Prohibited",
	"Host Precedence Violation",
	"Precedence Cutoff in Effect",
	"Destination Unreachable",
	"Time Exceeded",
	"Fragment Reassembly Time Exceeded",
	"Parameter Problem",
	"Missing Required Option",
	"Bad Length",
}

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

func portServiceName(port int, protocol string) string {
	if protos, ok := wellKnownServices[port]; ok {
		if name, ok := protos[protocol]; ok {
			return name
		}
	}
	return "Unknown"
}

func formatRTT(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%v", d.Round(time.Microsecond))
}

func matchesFilter(view stats.TargetView, filter string) bool {
	if filter == "" {
		return true
	}

	filter = strings.ToLower(filter)

	// Check for condition match like loss>0, rtt>100, jitter>10
	if strings.Contains(filter, ">") {
		parts := strings.Split(filter, ">")
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			valStr := strings.TrimSpace(parts[1])
			val, _ := strconv.ParseFloat(valStr, 64)
			switch key {
			case "loss":
				return calcLossRate(view) > val
			case "rtt":
				return float64(view.LastRTT.Milliseconds()) > val
			case "avg":
				return float64(view.AvgRTT.Milliseconds()) > val
			case "jitter":
				return float64(view.Jitter.Milliseconds()) > val
			}
		}
	} else if strings.Contains(filter, "<") {
		parts := strings.Split(filter, "<")
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			valStr := strings.TrimSpace(parts[1])
			val, _ := strconv.ParseFloat(valStr, 64)
			switch key {
			case "loss":
				return calcLossRate(view) < val
			case "rtt":
				return float64(view.LastRTT.Milliseconds()) < val
			case "avg":
				return float64(view.AvgRTT.Milliseconds()) < val
			case "jitter":
				return float64(view.Jitter.Milliseconds()) < val
			}
		}
	}

	// Default: text match on host, IP or ASN
	return strings.Contains(strings.ToLower(view.Host), filter) ||
		strings.Contains(strings.ToLower(view.IP), filter) ||
		strings.Contains(strings.ToLower(view.ASN), filter)
}

func calcLossRate(view stats.TargetView) float64 {
	totalAttempts := view.Recv + view.Loss
	if totalAttempts == 0 {
		return 0.0
	}
	return (float64(view.Loss) / float64(totalAttempts)) * 100
}

func formatLossAgo(lastLossTime time.Time) string {
	if lastLossTime.IsZero() {
		return "-"
	}
	ago := time.Since(lastLossTime).Round(time.Second)
	return fmt.Sprintf("%s ago", ago)
}

func ttlString(ttl int) string {
	if ttl <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", ttl)
}

// inferInitialTTL returns the likely initial TTL the remote host used,
// inferred from the received TTL (LastTTL).
// Common OS defaults: 64 (Linux/macOS), 128 (Windows), 255 (network devices).
func inferInitialTTL(lastTTL int) string {
	switch {
	case lastTTL <= 0:
		return "-"
	case lastTTL <= 64:
		return "64"
	case lastTTL <= 128:
		return "128"
	default:
		return "255"
	}
}

func hopCountString(hops []string) string {
	if len(hops) == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", len(hops))
}

// wrapHops splits hops into lines that fit within maxWidth display columns.
// Each hop is joined with " -> " and a new line is started when adding the
// next hop would exceed maxWidth.
func wrapHops(hops []string, maxWidth int) []string {
	if len(hops) == 0 || maxWidth <= 0 {
		return nil
	}
	var lines []string
	var current []string
	currentW := 0
	for _, hop := range hops {
		var segment string
		if len(current) == 0 {
			segment = hop
		} else {
			segment = " -> " + hop
		}
		segW := runewidth.StringWidth(segment)
		if len(current) > 0 && currentW+segW > maxWidth {
			lines = append(lines, strings.Join(current, " -> "))
			current = []string{hop}
			currentW = runewidth.StringWidth(hop)
		} else {
			current = append(current, hop)
			currentW += segW
		}
	}
	if len(current) > 0 {
		lines = append(lines, strings.Join(current, " -> "))
	}
	return lines
}

func mtuString(mtu int) string {
	if mtu <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", mtu)
}

func buildFullColumns(view stats.TargetView, sourceIPv4, sourceIPv6 string, packetSize int) ([]string, string, float64) {
	lossRate := calcLossRate(view)
	lossStr := formatLossAgo(view.LastLossTime)
	rttStr := formatRTT(view.LastRTT)
	avgStr := formatRTT(view.AvgRTT)
	jitterStr := formatRTT(view.Jitter)

	rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)

	dstDisplay := view.IP
	if view.Host != view.IP {
		dstDisplay = fmt.Sprintf("%s (%s)", view.Host, view.IP)
	}

	cols := []string{
		rowSourceIP,
		dstDisplay, // Dst IP
		view.ASN,   // ASN
		fmt.Sprintf("%d", view.Recv),
		fmt.Sprintf("%d", view.Loss),
		fmt.Sprintf("%.1f%%", lossRate),
		rttStr,
		avgStr,
		jitterStr,
		fmt.Sprintf("%d", packetSize),
		mtuString(view.IfaceMTU),
		ttlString(view.LastTTL),
		formatTableError(view.LastError),
		lossStr,
	}
	return cols, rowSourceIP, lossRate
}

func lossColorForRate(lossRate float64, vividRed tcell.Color) tcell.Color {
	if lossRate > lossRedThreshold {
		return vividRed
	}
	if lossRate > lossOrangeThreshold {
		return tcell.ColorOrange
	}
	return tcell.ColorGreen
}

func rttColorForRTT(rtt time.Duration, vividRed tcell.Color) tcell.Color {
	if rtt > rttRedThreshold {
		return vividRed
	}
	if rtt > rttOrangeThreshold {
		return tcell.ColorOrange
	}
	if rtt > 0 {
		return tcell.ColorGreen
	}
	return tcell.ColorWhite
}

func jitterColorForJitter(jitter time.Duration, vividRed tcell.Color) tcell.Color {
	if jitter > jitterRedThreshold {
		return vividRed
	}
	if jitter > jitterOrangeThreshold {
		return tcell.ColorOrange
	}
	if jitter > 0 {
		return tcell.ColorGreen
	}
	return tcell.ColorWhite
}

func formatTableError(err string) string {
	if err == "" {
		return ""
	}
	parts := strings.Split(err, ": ")
	return parts[len(parts)-1]
}

func calcInitialTableErrorWidth(targets []*stats.TargetStats, header string, minWidth int) int {
	maxWidth := runewidth.StringWidth(header)
	if minWidth > maxWidth {
		maxWidth = minWidth
	}
	for _, s := range tableErrorCandidates {
		if w := runewidth.StringWidth(s); w > maxWidth {
			maxWidth = w
		}
	}
	for _, t := range targets {
		if w := runewidth.StringWidth(formatTableError(t.GetView().LastError)); w > maxWidth {
			maxWidth = w
		}
	}
	return maxWidth
}

func truncateToDisplayWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	limit := width - 3
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if rw == 0 {
			rw = 1
		}
		if cur+rw > limit {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String() + "..."
}

func formatCellText(text string, width int, align int) string {
	if width <= 0 {
		return ""
	}
	text = truncateToDisplayWidth(text, width)
	textWidth := runewidth.StringWidth(text)
	if textWidth >= width {
		return text
	}
	pad := strings.Repeat(" ", width-textWidth)
	if align == tview.AlignRight {
		return pad + text
	}
	return text + pad
}

func fitWidthsToAvailable(desired, minWidths, maxWidths []int, availableColumnsWidth int) ([]int, bool) {
	if len(desired) != len(minWidths) || len(desired) != len(maxWidths) {
		return nil, false
	}
	widths := make([]int, len(desired))
	sumMin := 0
	for i := range desired {
		if minWidths[i] > maxWidths[i] {
			maxWidths[i] = minWidths[i]
		}
		w := desired[i]
		if w < minWidths[i] {
			w = minWidths[i]
		}
		if w > maxWidths[i] {
			w = maxWidths[i]
		}
		widths[i] = w
		sumMin += minWidths[i]
	}
	if availableColumnsWidth < sumMin {
		return nil, false
	}

	sum := 0
	for _, w := range widths {
		sum += w
	}

	shrinkOrder := []int{11, 1, 0, 12, 7, 6, 5, 4, 2, 3, 9, 10, 8}
	for sum > availableColumnsWidth {
		changed := false
		for _, idx := range shrinkOrder {
			if idx < 0 || idx >= len(widths) {
				continue
			}
			if widths[idx] > minWidths[idx] {
				widths[idx]--
				sum--
				changed = true
				if sum <= availableColumnsWidth {
					break
				}
			}
		}
		if !changed {
			break
		}
	}

	growOrder := []int{1, 0, 11, 12, 2, 5, 6, 7, 4, 3, 8, 9, 10}
	for sum < availableColumnsWidth {
		changed := false
		for _, idx := range growOrder {
			if idx < 0 || idx >= len(widths) {
				continue
			}
			if widths[idx] < maxWidths[idx] {
				widths[idx]++
				sum++
				changed = true
				if sum >= availableColumnsWidth {
					break
				}
			}
		}
		if !changed {
			break
		}
	}

	return widths, true
}

type compactRow struct {
	hostL string
	pathL string
	statL string
	errL  string
	hostR string
	pathR string
	statR string
	errR  string
}

type compactLayout struct {
	rows    []compactRow
	desired []int
	headers []string
	aligns  []int
	min     []int
	max     []int
}

type alertFlags struct {
	lossRed   bool
	rttRed    bool
	jitterRed bool
}

func buildCompactLayout(targets []*stats.TargetStats, packetSize int, sourceIPv4, sourceIPv6 string, errorMaxWidth int) compactLayout {
	headers := []string{"Host", "Path", "Stats", "Error"}
	aligns := []int{tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignLeft}
	desired := []int{
		runewidth.StringWidth(headers[0]),
		runewidth.StringWidth(headers[1]),
		runewidth.StringWidth(headers[2]),
		runewidth.StringWidth(headers[3]),
	}
	min := []int{8, 16, 18, 8}
	max := []int{40, 80, 80, errorMaxWidth}

	rows := make([]compactRow, 0, len(targets)*2)
	for _, t := range targets {
		view := t.GetView()
		lossRate := calcLossRate(view)
		lossStr := formatLossAgo(view.LastLossTime)
		rttStr := formatRTT(view.LastRTT)
		avgStr := formatRTT(view.AvgRTT)
		jitterStr := formatRTT(view.Jitter)
		mtuStr := mtuString(view.IfaceMTU)
		ttlStr := ttlString(view.LastTTL)
		rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
		errText := formatTableError(view.LastError)

		r1 := compactRow{
			hostL: view.Host,
			pathL: fmt.Sprintf("%s -> %s", rowSourceIP, view.IP),
			statL: fmt.Sprintf("S:%d L:%d Loss:%0.1f%% RTT:%s", view.Recv, view.Loss, lossRate, rttStr),
			errL:  errText,
		}
		r2 := compactRow{
			hostR: "Avg/Jit",
			pathR: "",
			statR: fmt.Sprintf("Avg:%s Jit:%s TTL:%s Sz:%d MTU:%s Last:%s", avgStr, jitterStr, ttlStr, packetSize, mtuStr, lossStr),
			errR:  "",
		}
		rows = append(rows, r1, r2)

		for i, v := range []string{r1.hostL, r1.pathL, r1.statL, r1.errL, r2.hostR, r2.pathR, r2.statR, r2.errR} {
			col := i % 4
			if w := runewidth.StringWidth(v); w > desired[col] {
				desired[col] = w
			}
		}
	}

	return compactLayout{
		rows:    rows,
		desired: desired,
		headers: headers,
		aligns:  aligns,
		min:     min,
		max:     max,
	}
}

func displaySourceIPForDst(dstIP, sourceIPv4, sourceIPv6 string) string {
	dst := dstIP
	if i := strings.Index(dst, "%"); i >= 0 {
		dst = dst[:i]
	}
	if ip := net.ParseIP(dst); ip != nil {
		if ip.To4() != nil {
			if sourceIPv4 != "" {
				return sourceIPv4
			}
			return "Auto"
		}
		if sourceIPv6 != "" {
			return sourceIPv6
		}
		return "Auto"
	}
	if strings.Contains(dstIP, ":") {
		if sourceIPv6 != "" {
			return sourceIPv6
		}
		return "Auto"
	}
	if sourceIPv4 != "" {
		return sourceIPv4
	}
	return "Auto"
}

func normalizeWriteIP(errMsg, sourceIP string) string {
	if sourceIP == "" || sourceIP == "Auto" {
		return errMsg
	}
	if strings.Contains(errMsg, "write ip 0.0.0.0->") {
		return strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+sourceIP+"->", 1)
	}
	return errMsg
}

func buildErrorLogMessage(view stats.TargetView, sourceIP string, errMsg string, ts time.Time) string {
	msg := normalizeWriteIP(errMsg, sourceIP)
	return fmt.Sprintf("[red][%s] %s (%s): %s[-]", ts.Format("15:04:05"), view.Host, sourceIP, msg)
}

func updateAlertState(view stats.TargetView, sourceIP string, lossRate float64, now time.Time, state alertFlags) (alertFlags, []string) {
	var msgs []string
	if lossRate > lossRedThreshold {
		if !state.lossRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): Loss Ratio %.1f%%[-]", now.Format("15:04:05"), view.Host, sourceIP, lossRate))
		}
		state.lossRed = true
	} else {
		state.lossRed = false
	}

	if view.LastRTT > rttRedThreshold {
		if !state.rttRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): RTT %v[-]", now.Format("15:04:05"), view.Host, sourceIP, view.LastRTT.Round(time.Microsecond)))
		}
		state.rttRed = true
	} else {
		state.rttRed = false
	}

	if view.Jitter > jitterRedThreshold {
		if !state.jitterRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): Jitter %v[-]", now.Format("15:04:05"), view.Host, sourceIP, view.Jitter.Round(time.Microsecond)))
		}
		state.jitterRed = true
	} else {
		state.jitterRed = false
	}

	return state, msgs
}

func buildFullRowCells(cols []string, widths []int, aligns []int, lossRate float64, rtt time.Duration, jitter time.Duration, vividRed tcell.Color, rowColor tcell.Color) []*tview.TableCell {
	cells := make([]*tview.TableCell, len(cols))
	lossColor := lossColorForRate(lossRate, vividRed)
	rttColor := rttColorForRTT(rtt, vividRed)
	jitterColor := jitterColorForJitter(jitter, vividRed)

	for c, col := range cols {
		text := formatCellText(col, widths[c], aligns[c])
		cell := tview.NewTableCell(text).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(rowColor).
			SetAlign(aligns[c])

		switch c {
		case 4: // Loss Ratio column index
			cell.SetTextColor(lossColor).SetAttributes(tcell.AttrBold)
		case 5: // RTT column index
			cell.SetTextColor(rttColor)
		case 7: // Jitter column index
			cell.SetTextColor(jitterColor)
		case 11: // Error column
			if text != "" {
				cell.SetTextColor(vividRed)
			}
		}

		cells[c] = cell
	}
	return cells
}

func buildCompactRowCells(values []string, widths []int, aligns []int, vividRed tcell.Color, rowColor tcell.Color) []*tview.TableCell {
	cells := make([]*tview.TableCell, len(values))
	for c, v := range values {
		cell := tview.NewTableCell(formatCellText(v, widths[c], aligns[c])).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(rowColor).
			SetAlign(aligns[c])
		if c == 0 {
			cell.SetTextColor(tcell.ColorWhite)
		} else if c == 3 && strings.TrimSpace(v) != "" {
			cell.SetTextColor(vividRed)
		}
		cells[c] = cell
	}
	return cells
}

func appendErrorLog(errorLogs *[]string, errorView *tview.TextView, msg string) {
	*errorLogs = append(*errorLogs, msg)
	if len(*errorLogs) > errorLogMaxSize {
		*errorLogs = (*errorLogs)[1:]
	}
	errorView.SetText(strings.Join(*errorLogs, "\n") + "\n")
	errorView.ScrollToEnd()
}

// paddedCell returns text with a leading space, right-padded to fill colW.
func paddedCell(text string, colW int) string {
	return formatCellText(" "+text, colW, tview.AlignLeft)
}

// statusColorTag returns a tview color tag for the given port status.
func statusColorTag(status string) string {
	switch status {
	case "Open":
		return "[green]"
	case "Closed":
		return "[red]"
	case "Filtered", "Open|Filtered":
		return "[yellow]"
	default:
		return "[white]"
	}
}

// renderTracerouteTable builds the traceroute monitor table string.
func renderTracerouteTable(targets []*stats.TargetStats, availW int) string {
	const minRouteContentW = 20

	hostColW := runewidth.StringWidth("Host")
	for _, t := range targets {
		if w := runewidth.StringWidth(t.GetView().Host); w > hostColW {
			hostColW = w
		}
	}
	hostColW += 2

	hopsColW := runewidth.StringWidth("Hops") + 2
	initTTLColW := runewidth.StringWidth("Init TTL") + 2

	fullRouteContentW := availW - hostColW - hopsColW - initTTLColW - 5
	traceCompact := fullRouteContentW < minRouteContentW

	dataTargets := make([]*stats.TargetStats, 0, len(targets))
	for _, t := range targets {
		if len(t.GetView().TraceHops) > 0 {
			dataTargets = append(dataTargets, t)
		}
	}

	var sb strings.Builder
	h := strings.Repeat("─", hostColW)

	if traceCompact {
		routeColW := availW - hostColW - 3
		if routeColW < minRouteContentW {
			routeColW = minRouteContentW
		}
		routeContentW := routeColW - 1
		r := strings.Repeat("─", routeColW)

		fmt.Fprintf(&sb, "[white]┌%s┬%s┐[-]\n", h, r)
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			paddedCell("Host", hostColW), paddedCell("Route", routeColW))
		fmt.Fprintf(&sb, "[white]├%s┼%s┤[-]\n", h, r)

		emptyHost := paddedCell("", hostColW)
		for i, t := range dataTargets {
			view := t.GetView()
			routeLines := wrapHops(view.TraceHops, routeContentW)
			if len(routeLines) == 0 {
				routeLines = []string{""}
			}
			fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[-]\n",
				paddedCell(view.Host, hostColW), paddedCell(routeLines[0], routeColW))
			for _, rl := range routeLines[1:] {
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					emptyHost, paddedCell(rl, routeColW))
			}
			if i < len(dataTargets)-1 {
				fmt.Fprintf(&sb, "[white]├%s┼%s┤[-]\n", h, r)
			}
		}
		fmt.Fprintf(&sb, "[white]└%s┴%s┘[-]\n", h, r)
	} else {
		routeColW := fullRouteContentW
		ho := strings.Repeat("─", hopsColW)
		it := strings.Repeat("─", initTTLColW)
		r := strings.Repeat("─", routeColW)
		routeContentW := routeColW - 1

		fmt.Fprintf(&sb, "[white]┌%s┬%s┬%s┬%s┐[-]\n", h, ho, it, r)
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			paddedCell("Host", hostColW), paddedCell("Hops", hopsColW), paddedCell("Init TTL", initTTLColW), paddedCell("Route", routeColW))
		fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┤[-]\n", h, ho, it, r)

		emptyHost := paddedCell("", hostColW)
		emptyHops := paddedCell("", hopsColW)
		emptyInitTTL := paddedCell("", initTTLColW)
		for i, t := range dataTargets {
			view := t.GetView()
			hopsStr := hopCountString(view.TraceHops)
			initTTLStr := inferInitialTTL(view.LastTTL)
			routeLines := wrapHops(view.TraceHops, routeContentW)
			if len(routeLines) == 0 {
				routeLines = []string{""}
			}
			fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[-]\n",
				paddedCell(view.Host, hostColW), paddedCell(hopsStr, hopsColW), paddedCell(initTTLStr, initTTLColW), paddedCell(routeLines[0], routeColW))
			for _, rl := range routeLines[1:] {
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					emptyHost, emptyHops, emptyInitTTL, paddedCell(rl, routeColW))
			}
			if i < len(dataTargets)-1 {
				fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┤[-]\n", h, ho, it, r)
			}
		}
		fmt.Fprintf(&sb, "[white]└%s┴%s┴%s┴%s┘[-]\n", h, ho, it, r)
	}

	return sb.String()
}

// renderPortMonitorTable builds the port monitor table string.
// It also detects status changes and appends log messages.
func renderPortMonitorTable(targets []*stats.TargetStats, availW int, lastPortStatuses map[string]string, errorLogs *[]string, errorView *tview.TextView) string {
	targetColW := runewidth.StringWidth("Target")
	for _, t := range targets {
		if w := runewidth.StringWidth(t.GetView().Host); w > targetColW {
			targetColW = w
		}
	}
	targetColW += 2

	portColW := runewidth.StringWidth("Port")
	for _, t := range targets {
		for _, pr := range t.GetView().PortResults {
			if w := runewidth.StringWidth(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol)); w > portColW {
				portColW = w
			}
		}
	}
	portColW += 2

	serviceColW := runewidth.StringWidth("Service")
	for _, t := range targets {
		for _, pr := range t.GetView().PortResults {
			if w := runewidth.StringWidth(portServiceName(pr.Port, pr.Protocol)); w > serviceColW {
				serviceColW = w
			}
		}
	}
	serviceColW += 2

	statusColW := runewidth.StringWidth("Open|Filtered") + 2
	rttColW := runewidth.StringWidth("RTT")
	for _, t := range targets {
		for _, pr := range t.GetView().PortResults {
			if w := runewidth.StringWidth(formatRTT(pr.RTT)); w > rttColW {
				rttColW = w
			}
		}
	}
	rttColW += 2
	countColW := runewidth.StringWidth("Open/Closed") + 2
	changeColW := runewidth.StringWidth("Last Change") + 2

	usedFull := targetColW + portColW + serviceColW + statusColW + rttColW + countColW + changeColW + 8
	const minPortContentW = 20
	portCompact := availW-targetColW-portColW-statusColW-rttColW-5 < minPortContentW

	// Collect targets that have results; detect status changes
	dataTargets := make([]*stats.TargetStats, 0, len(targets))
	for _, t := range targets {
		view := t.GetView()
		if len(view.PortResults) == 0 {
			continue
		}
		dataTargets = append(dataTargets, t)
		for _, pr := range view.PortResults {
			if pr.Status == "" || pr.Status == "Checking..." {
				continue
			}
			key := fmt.Sprintf("%s|%d/%s", view.Host, pr.Port, pr.Protocol)
			prev, seen := lastPortStatuses[key]
			if seen && prev != pr.Status {
				color := "[yellow]"
				if pr.Status == "Open" {
					color = "[green]"
				}
				now := time.Now()
				msg := fmt.Sprintf("[darkgray]%s[-] %s%s[-] [white]%d/%s:[white] %s → %s%s[-]",
					now.Format("15:04:05"), "[white]", view.Host, pr.Port, pr.Protocol,
					prev, color, pr.Status)
				appendErrorLog(errorLogs, errorView, msg)
			}
			lastPortStatuses[key] = pr.Status
		}
	}

	var sb strings.Builder

	if portCompact {
		th := strings.Repeat("─", targetColW)
		ph := strings.Repeat("─", portColW)
		sh := strings.Repeat("─", statusColW)
		rh := strings.Repeat("─", rttColW)

		fmt.Fprintf(&sb, "[white]┌%s┬%s┬%s┬%s┐[-]\n", th, ph, sh, rh)
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			paddedCell("Target", targetColW), paddedCell("Port", portColW),
			paddedCell("Status", statusColW), paddedCell("RTT", rttColW))
		fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┤[-]\n", th, ph, sh, rh)

		rowCount := 0
		for ti, t := range dataTargets {
			view := t.GetView()
			for i, pr := range view.PortResults {
				targetName := ""
				if i == 0 {
					targetName = view.Host
				}
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│[-]\n",
					paddedCell(targetName, targetColW),
					paddedCell(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol), portColW),
					statusColorTag(pr.Status), paddedCell(pr.Status, statusColW),
					paddedCell(formatRTT(pr.RTT), rttColW))
				rowCount++
			}
			if ti < len(dataTargets)-1 {
				fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┤[-]\n", th, ph, sh, rh)
			}
		}
		if rowCount == 0 {
			total := targetColW + portColW + statusColW + rttColW + 3
			fmt.Fprintf(&sb, "[white]│[darkgray]%s[white]│[-]\n",
				formatCellText(" Waiting for results...", total, tview.AlignLeft))
		}
		fmt.Fprintf(&sb, "[white]└%s┴%s┴%s┴%s┘[-]\n", th, ph, sh, rh)
	} else {
		if availW > usedFull {
			changeColW += availW - usedFull
		}

		th := strings.Repeat("─", targetColW)
		ph := strings.Repeat("─", portColW)
		svh := strings.Repeat("─", serviceColW)
		sh := strings.Repeat("─", statusColW)
		rh := strings.Repeat("─", rttColW)
		cch := strings.Repeat("─", countColW)
		lh := strings.Repeat("─", changeColW)

		fmt.Fprintf(&sb, "[white]┌%s┬%s┬%s┬%s┬%s┬%s┬%s┐[-]\n", th, ph, svh, sh, rh, cch, lh)
		fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
			paddedCell("Target", targetColW), paddedCell("Port", portColW), paddedCell("Service", serviceColW),
			paddedCell("Status", statusColW), paddedCell("RTT", rttColW),
			paddedCell("Open/Closed", countColW), paddedCell("Last Change", changeColW))
		fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┼%s┤[-]\n", th, ph, svh, sh, rh, cch, lh)

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
					targetName = view.Host
				}
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					paddedCell(targetName, targetColW),
					paddedCell(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol), portColW),
					paddedCell(portServiceName(pr.Port, pr.Protocol), serviceColW),
					statusColorTag(pr.Status), paddedCell(pr.Status, statusColW),
					paddedCell(formatRTT(pr.RTT), rttColW),
					paddedCell(countStr, countColW),
					paddedCell(changeStr, changeColW))
				rowCount++
			}
			if ti < len(dataTargets)-1 {
				fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┼%s┤[-]\n", th, ph, svh, sh, rh, cch, lh)
			}
		}
		if rowCount == 0 {
			total := targetColW + portColW + serviceColW + statusColW + rttColW + countColW + changeColW + 6
			fmt.Fprintf(&sb, "[white]│[darkgray]%s[white]│[-]\n",
				formatCellText(" Waiting for results...", total, tview.AlignLeft))
		}
		fmt.Fprintf(&sb, "[white]└%s┴%s┴%s┴%s┴%s┴%s┴%s┘[-]\n", th, ph, svh, sh, rh, cch, lh)
	}

	return sb.String()
}

// makeDoubleBorderDrawFunc creates a SetDrawFunc callback that draws a
// double-line (╔═╗║╚═╝) border with a centered title.
// borderColor is a pointer so the caller can change the color dynamically.
func makeDoubleBorderDrawFunc(title string, borderColor *tcell.Color) func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
	return func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		if width < 2 || height < 2 {
			return x + 1, y + 1, width - 2, height - 2
		}
		style := tcell.StyleDefault.Foreground(*borderColor)
		screen.SetContent(x, y, '╔', nil, style)
		for i := x + 1; i < x+width-1; i++ {
			screen.SetContent(i, y, '═', nil, style)
		}
		screen.SetContent(x+width-1, y, '╗', nil, style)
		screen.SetContent(x, y+height-1, '╚', nil, style)
		for i := x + 1; i < x+width-1; i++ {
			screen.SetContent(i, y+height-1, '═', nil, style)
		}
		screen.SetContent(x+width-1, y+height-1, '╝', nil, style)
		for i := y + 1; i < y+height-1; i++ {
			screen.SetContent(x, i, '║', nil, style)
			screen.SetContent(x+width-1, i, '║', nil, style)
		}
		tview.Print(screen, title, x+1, y, width-2, tview.AlignCenter, *borderColor)
		return x + 1, y + 1, width - 2, height - 2
	}
}

