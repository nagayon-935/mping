package ui

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	errorLogMaxSize      = 1000 // maximum number of lines kept in the error log pane
	minRouteContentWidth = 20
	minPortContentWidth  = 20
)

var (
	vividRed  = tcell.NewRGBColor(255, 0, 0)
	vividCyan = tcell.NewRGBColor(0, 255, 255)
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

func buildFullColumns(view stats.TargetView, sourceIPv4, sourceIPv6 string, packetSize int, asnEnabled bool, dnsEnabled bool) ([]string, string, float64) {
	lossRate := calcLossRate(view)
	lossStr := formatLossAgo(view.LastLossTime)
	rttStr := formatRTT(view.LastRTT)
	avgStr := formatRTT(view.AvgRTT)
	jitterStr := formatRTT(view.Jitter)

	rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)

	dstDisplay := view.Host
	if view.Host != view.IP && !strings.Contains(view.Host, " ("+view.IP+")") {
		dstDisplay = fmt.Sprintf("%s (%s)", view.Host, view.IP)
	}

	cols := []string{
		rowSourceIP,
		dstDisplay, // Dst IP
	}
	if dnsEnabled {
		cols = append(cols, view.DNSServer)
	}
	if asnEnabled {
		asnCol := view.ASN
		if view.Country != "" {
			asnCol += " " + view.Country
		}
		if view.Org != "" {
			asnCol += " " + view.Org
		}
		cols = append(cols, asnCol)
	}
	cols = append(cols,
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
	)
	return cols, rowSourceIP, lossRate
}

func lossColorForRate(lossRate float64) tcell.Color {
	th := getActiveThresholds()
	if lossRate > th.LossCrit {
		return vividRed
	}
	if lossRate > th.LossWarn {
		return tcell.ColorOrange
	}
	return tcell.ColorGreen
}

func rttColorForRTT(rtt time.Duration) tcell.Color {
	th := getActiveThresholds()
	if rtt > th.RTTCrit {
		return vividRed
	}
	if rtt > th.RTTWarn {
		return tcell.ColorOrange
	}
	if rtt > 0 {
		return tcell.ColorGreen
	}
	return tcell.ColorWhite
}

func jitterColorForJitter(jitter time.Duration) tcell.Color {
	th := getActiveThresholds()
	if jitter > th.JitterCrit {
		return vividRed
	}
	if jitter > th.JitterWarn {
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
	th := getActiveThresholds()
	var msgs []string
	if lossRate > th.LossCrit {
		if !state.lossRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): Loss Ratio %.1f%%[-]", now.Format("15:04:05"), view.Host, sourceIP, lossRate))
		}
		state.lossRed = true
	} else {
		state.lossRed = false
	}

	if view.LastRTT > th.RTTCrit {
		if !state.rttRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): RTT %v[-]", now.Format("15:04:05"), view.Host, sourceIP, view.LastRTT.Round(time.Microsecond)))
		}
		state.rttRed = true
	} else {
		state.rttRed = false
	}

	if view.Jitter > th.JitterCrit {
		if !state.jitterRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): Jitter %v[-]", now.Format("15:04:05"), view.Host, sourceIP, view.Jitter.Round(time.Microsecond)))
		}
		state.jitterRed = true
	} else {
		state.jitterRed = false
	}

	return state, msgs
}

func buildFullRowCells(cols []string, widths []int, aligns []int, lossRate float64, rtt time.Duration, jitter time.Duration, rowColor tcell.Color, asnEnabled bool, dnsEnabled bool) []*tview.TableCell {
	cells := make([]*tview.TableCell, len(cols))
	lossColor := lossColorForRate(lossRate)
	rttColor := rttColorForRTT(rtt)
	jitterColor := jitterColorForJitter(jitter)

	for c, col := range cols {
		text := formatCellText(col, widths[c], aligns[c])
		cell := tview.NewTableCell(text).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(rowColor).
			SetAlign(aligns[c])

		offset := 0
		if dnsEnabled {
			offset++
		}
		if asnEnabled {
			offset++
		}

		switch c {
		case 2 + offset: // Success column index
			// no special color
		case 3 + offset: // Loss column index
			// no special color
		case 4 + offset: // Loss Ratio column index
			cell.SetTextColor(lossColor).SetAttributes(tcell.AttrBold)
		case 5 + offset: // RTT column index
			cell.SetTextColor(rttColor)
		case 7 + offset: // Jitter column index
			cell.SetTextColor(jitterColor)
		case len(cols) - 2: // Error column
			if text != "" {
				cell.SetTextColor(vividRed)
			}
		}

		cells[c] = cell
	}
	return cells
}

func buildCompactRowCells(values []string, widths []int, aligns []int, rowColor tcell.Color) []*tview.TableCell {
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
		// Rebuild once on eviction instead of every call.
		*errorLogs = (*errorLogs)[1:]
		errorView.SetText(strings.Join(*errorLogs, "\n") + "\n")
		errorView.ScrollToEnd()
		return
	}
	fmt.Fprintf(errorView, "%s\n", msg)
	errorView.ScrollToEnd()
}

// paddedCell returns text with a leading space, right-padded to fill colW.
func paddedCell(text string, colW int) string {
	return formatCellText(" "+text, colW, tview.AlignLeft)
}

// rightPaddedCell returns text with a trailing space, left-padded to fill colW.
func rightPaddedCell(text string, colW int) string {
	if text == "" {
		return formatCellText("", colW, tview.AlignRight)
	}
	return formatCellText(text+" ", colW, tview.AlignRight)
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
