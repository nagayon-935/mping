package ui

import (
	"fmt"
	"github.com/nagayon-935/mping/internal/stats"
	"net"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

const (
	graphMaxVisibleRows = 3
	graphLabelWidth     = 6 // e.g. "100ms"
	graphMinWidth       = 10
	graphWindowSeconds  = 30
	errorLogVisibleRows = 15
	errorLogBoxHeight   = errorLogVisibleRows + 2
	// tableMaxRows is for the main stats table
	tableMaxRows    = 10
	tablePaneHeight = ((tableMaxRows + 1) * 2) + 1 + 2 // table height + pane border

	rttOrangeThreshold    = 50 * time.Millisecond
	rttRedThreshold       = 200 * time.Millisecond
	jitterOrangeThreshold = 10 * time.Millisecond
	jitterRedThreshold    = 50 * time.Millisecond
	lossOrangeThreshold   = 20.0
	lossRedThreshold      = 80.0

	graphYMax         = 100 * time.Millisecond // fixed Y-axis upper bound for the RTT graph
	errorLogMaxSize   = 1000                   // maximum number of lines kept in the error log pane
	minUIRefresh      = 200 * time.Millisecond // minimum UI refresh interval when ping interval is very short
	fastUIRefresh     = 100 * time.Millisecond // UI refresh rate used when ping interval < minUIRefresh
)

var graphGridValues = []int{25, 50, 75, 100}

var newApplication = tview.NewApplication

// Known short error texts that can be displayed in the table Error column.
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

// GraphView is a custom primitive for rendering RTT graphs
type GraphView struct {
	*tview.Box
	targets   []*stats.TargetStats
	interval  time.Duration
	vividCyan tcell.Color
	vividRed  tcell.Color
	scrollRow int
	showZero  bool
}

func NewGraphView(targets []*stats.TargetStats, interval time.Duration) *GraphView {
	return &GraphView{
		Box:       tview.NewBox(),
		targets:   targets,
		interval:  interval,
		vividCyan: tcell.NewRGBColor(0, 255, 255),
		vividRed:  tcell.NewRGBColor(255, 0, 0),
	}
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


func projectDurationsToGraph(data []time.Duration, windowPoints, graphWidth int) ([]time.Duration, []bool) {
	if windowPoints <= 0 || graphWidth <= 0 || len(data) == 0 {
		return nil, nil
	}
	if len(data) > windowPoints {
		data = data[len(data)-windowPoints:]
	}

	values := make([]time.Duration, graphWidth)
	hasValue := make([]bool, graphWidth)
	if windowPoints == 1 {
		values[graphWidth-1] = data[len(data)-1]
		hasValue[graphWidth-1] = true
		return values, hasValue
	}

	offset := windowPoints - len(data)
	prevSet := false
	prevX := 0
	prevV := time.Duration(0)

	for i, v := range data {
		windowIdx := offset + i
		x := int(float64(windowIdx)*float64(graphWidth-1)/float64(windowPoints-1) + 0.5)
		if x < 0 {
			x = 0
		}
		if x >= graphWidth {
			x = graphWidth - 1
		}
		if !prevSet {
			values[x] = v
			hasValue[x] = true
			prevSet = true
			prevX = x
			prevV = v
			continue
		}

		if x <= prevX {
			values[x] = v
			hasValue[x] = true
			prevX = x
			prevV = v
			continue
		}

		dx := x - prevX
		dv := v - prevV
		for p := 0; p <= dx; p++ {
			ratio := float64(p) / float64(dx)
			interp := prevV + time.Duration(float64(dv)*ratio)
			px := prevX + p
			values[px] = interp
			hasValue[px] = true
		}
		prevX = x
		prevV = v
	}
	return values, hasValue
}

func (g *GraphView) clampScroll(numRowsTotal, visibleRows int) {
	maxScroll := numRowsTotal - visibleRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if g.scrollRow < 0 {
		g.scrollRow = 0
	} else if g.scrollRow > maxScroll {
		g.scrollRow = maxScroll
	}
}

func adjustPlotArea(graphY, graphHeight int) (plotY, plotHeight int) {
	plotHeight = graphHeight
	plotY = graphY
	if plotHeight > 1 {
		// Ensure equal spacing by making (plotHeight-1) divisible by 4.
		desiredSteps := ((plotHeight - 1) / 4) * 4
		if desiredSteps < 1 {
			desiredSteps = 1
		}
		plotHeight = desiredSteps + 1
		plotY = graphY + (graphHeight - plotHeight)
	}
	if plotHeight > 1 {
		// Shift the plot up by one line when possible.
		plotY--
		if plotY < graphY {
			plotY = graphY
		}
		if plotY+plotHeight > graphY+graphHeight {
			plotHeight = (graphY + graphHeight) - plotY
		}
	}
	return plotY, plotHeight
}

func gridStepsForHeight(plotHeight int) (totalSteps, gy25, gy50, gy75, gy100 int) {
	totalSteps = plotHeight - 1
	if totalSteps < 1 {
		totalSteps = 1
	}
	baseStep := totalSteps / 4
	rem := totalSteps % 4
	seg := [4]int{baseStep, baseStep, baseStep, baseStep}
	for i := 0; i < rem; i++ {
		seg[i]++
	}
	gy25 = seg[0]
	gy50 = seg[0] + seg[1]
	gy75 = seg[0] + seg[1] + seg[2]
	gy100 = totalSteps
	return totalSteps, gy25, gy50, gy75, gy100
}

func (g *GraphView) layout(width, height int) (numCols, numRowsTotal, visibleRows, colWidth, rowHeight int) {
	numTargets := len(g.targets)
	if numTargets == 0 || width <= 0 || height <= 0 {
		return 1, 0, 0, 0, 0
	}

	numCols = 1
	if numTargets > 1 {
		numCols = 2
	}
	minCellWidth := graphMinWidth + graphLabelWidth + 2
	if numCols == 2 && width < minCellWidth*2 {
		numCols = 1
	}

	numRowsTotal = (numTargets + numCols - 1) / numCols

	visibleRows = numRowsTotal
	if visibleRows > graphMaxVisibleRows {
		visibleRows = graphMaxVisibleRows
	}
	if visibleRows < 1 {
		visibleRows = 1
	}

	colWidth = width / numCols
	rowHeight = height / visibleRows
	if rowHeight < 2 {
		rowHeight = 2
	}
	for visibleRows > 1 {
		graphHeight := rowHeight - 2
		if graphHeight >= 5 {
			break
		}
		visibleRows--
		if visibleRows < 1 {
			visibleRows = 1
			break
		}
		rowHeight = height / visibleRows
		if rowHeight < 2 {
			rowHeight = 2
		}
	}

	return numCols, numRowsTotal, visibleRows, colWidth, rowHeight
}

// InputHandler enables vertical scrolling when focused.
func (g *GraphView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyUp:
			g.scrollRow--
		case tcell.KeyDown:
			g.scrollRow++
		case tcell.KeyPgUp:
			g.scrollRow -= 3
		case tcell.KeyPgDn:
			g.scrollRow += 3
		default:
			return
		}

		_, _, width, height := g.GetInnerRect()
		if width <= 0 || height <= 0 {
			return
		}

		_, numRowsTotal, visibleRows, _, _ := g.layout(width, height)
		if visibleRows == 0 {
			g.scrollRow = 0
			return
		}
		g.clampScroll(numRowsTotal, visibleRows)
	}
}

// Draw implements tview.Primitive
func (g *GraphView) Draw(screen tcell.Screen) {
	g.Box.Draw(screen)
	x, y, width, height := g.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	numTargets := len(g.targets)
	if numTargets == 0 {
		return
	}

	numCols, numRowsTotal, visibleRows, colWidth, rowHeight := g.layout(width, height)
	if visibleRows == 0 {
		return
	}
	g.clampScroll(numRowsTotal, visibleRows)

	// Draw loop
	for r := 0; r < visibleRows; r++ {
		rowIndex := g.scrollRow + r
		// Y position for this row
		baseY := y + (r * rowHeight)
		if baseY >= y+height {
			break
		}

		// Max height for graph in this row block
		// Reserve 1 line for header text and 1 blank line between blocks.
		graphHeight := rowHeight - 2
		if graphHeight < 1 {
			graphHeight = 1
		}
		// Cap graphHeight to not overflow view
		if baseY+1+graphHeight > y+height {
			graphHeight = (y + height) - (baseY + 1)
		}

		for c := 0; c < numCols; c++ {
			idx := rowIndex*numCols + c
			if idx >= numTargets {
				break
			}

			// X position for this column
			baseX := x + (c * colWidth)

			// Target data
			t := g.targets[idx]
			view := t.GetView()

			// Draw Header: Hostname RTT
			headerStr := fmt.Sprintf("% -20s %s", view.Host, formatRTT(view.LastRTT))

			// Draw header string char by char
			printX := baseX
			tview.Print(screen, headerStr, printX, baseY, colWidth-2, tview.AlignLeft, tcell.ColorYellow)

			// Draw Graph
			// Calculate graph area
			graphX := baseX
			graphY := baseY + 1
			labelWidth := graphLabelWidth
			// graphWidth: reserve space for labels on the right
			graphWidth := colWidth - labelWidth - 2
			if graphWidth < graphMinWidth {
				graphWidth = graphMinWidth
			}

			// Time based limit (0-60s window)
			timeBasedWidth := int(graphWindowSeconds * time.Second / g.interval)
			if timeBasedWidth < 1 {
				timeBasedWidth = 1
			}

			// Render graph data for a fixed 30s window and project it onto current width.
			data, hasData := projectDurationsToGraph(view.History, timeBasedWidth, graphWidth)

			// Y-Axis fixed to 0-graphYMax
			const yMax = graphYMax
			const yMin = 0
			const yMaxMs = 100.0

			plotY, plotHeight := adjustPlotArea(graphY, graphHeight)

			rangeVal := float64(yMax - yMin)

			// Draw Grid Lines (25, 50, 75, 100 ms) with equal spacing for any height.
			gridYPos := make(map[int]bool)

			totalSteps, gy25, gy50, gy75, gy100 := gridStepsForHeight(plotHeight)

			for _, val := range graphGridValues {
				gy := 0
				switch val {
				case 25:
					gy = gy25
				case 50:
					gy = gy50
				case 75:
					gy = gy75
				case 100:
					gy = gy100
				default:
					gy = int(float64(val) / 100.0 * float64(totalSteps))
				}

				// Calculate screen Y (py)
				// gy=0 is bottom. py = graphY + height - 1 - gy
				py := plotY + (plotHeight - 1 - gy)

				if py >= plotY && py < plotY+plotHeight {
					gridYPos[gy] = true

					// Draw grid line
					for gx := 0; gx < graphWidth; gx++ {
						screen.SetContent(graphX+gx, py, '·', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
					}
					// Draw label
					tview.Print(screen, fmt.Sprintf("%dms", val), graphX+graphWidth+1, py, labelWidth, tview.AlignLeft, tcell.ColorGray)
				}
			}

			// Label for 0ms (Bottom)
			bottomY := plotY + plotHeight - 1
			tview.Print(screen, "0ms", graphX+graphWidth+1, bottomY, labelWidth, tview.AlignLeft, tcell.ColorGray)

			// Label for 100ms (Top) is drawn via grid line to avoid duplicate text.

			if len(data) > 0 {
				// Plot columns
				chars := []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
				for i, val := range data {
					if !hasData[i] {
						continue
					}
					px := graphX + i
					// Cap value to yMax
					v := val
					if v > yMax {
						v = yMax
					}

					ratio := float64(v-yMin) / rangeVal
					if v > 0 && ratio < 0.05 {
						ratio = 0.05
					}

					totalLevels := int(ratio * float64(plotHeight*8))
					if v > 0 && totalLevels == 0 {
						totalLevels = 1
					}

					for gy := 0; gy < plotHeight; gy++ {
						py := plotY + (plotHeight - 1 - gy) // Draw from bottom up
						level := totalLevels - (gy * 8)

						var r rune
						if level <= 0 {
							// If empty, check if it's a grid line position
							if gridYPos[gy] {
								r = '·' // Grid line char
							} else {
								r = ' '
							}
						} else if level >= 8 {
							r = '█'
						} else {
							r = chars[level]
						}

						// Draw on screen
						if r != ' ' {
							color := g.vividCyan
							if r == '·' {
								color = tcell.ColorGray
							}
							screen.SetContent(px, py, r, nil, tcell.StyleDefault.Foreground(color))
						}
					}
				}
			}

			// Separator line between host graph blocks
			sepY := baseY + rowHeight - 1
			if sepY > graphY && sepY < y+height {
				for sx := 0; sx < colWidth; sx++ {
					screen.SetContent(baseX+sx, sepY, '─', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
				}
			}

		}
	}
}

func Run(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error) error {
	// Define vivid colors
	vividRed := tcell.NewRGBColor(255, 0, 0)
	vividCyan := tcell.NewRGBColor(0, 255, 255)

	app := newApplication()
	table := tview.NewTable().
		SetBorders(true).
		SetSelectable(false, false).
		SetFixed(1, 1)

	// Use custom GraphView
	graphView := NewGraphView(targets, interval)
	graphView.SetBorder(true).SetTitle(" RTT Graphs ").SetTitleColor(vividCyan).SetBorderColor(vividCyan)
	graphView.SetBackgroundColor(tcell.ColorBlack)

	errorView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true). // Ensure long messages wrap
		SetChangedFunc(func() {
			app.Draw()
		})
	errorView.SetBorder(true).SetTitle(" Log ").SetTitleColor(tcell.ColorRed).SetBorderColor(tcell.ColorRed)
	errorView.SetBackgroundColor(tcell.ColorBlack)

	// Set black background and darkgray borders
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetBorderColor(tcell.ColorWhite)
	table.SetBordersColor(tcell.ColorWhite)


	// Columns: Src IP, Dst IP, Success, Loss, Loss Ratio, RTT, Avg, Jitter, Size, MTU, TTL, Error, Last Loss
	fullHeaders := []string{"Src IP", "Dst IP", "Success", "Loss", "Loss Ratio", "RTT", "Avg", "Jitter", "Size", "MTU", "TTL", "Error", "Last Loss"}
	fullAligns := []int{
		tview.AlignLeft, tview.AlignLeft, tview.AlignRight, tview.AlignRight, tview.AlignRight,
		tview.AlignRight, tview.AlignRight, tview.AlignRight, // RTTs
		tview.AlignRight, tview.AlignRight, tview.AlignRight, tview.AlignLeft, tview.AlignLeft,
	}
	// Src IP / Dst IP are dynamically resized from the rendered content.
	// Error width is fixed at startup to prevent table size jumps when new errors arrive.
	baseWidths := []int{6, 6, 8, 7, 10, 10, 10, 10, 6, 6, 5, 30, 15}
	baseWidths[11] = calcInitialTableErrorWidth(targets, fullHeaders[11], baseWidths[11])
	minWidths := []int{4, 8, 5, 4, 6, 7, 7, 7, 4, 4, 3, 8, 8}
	maxWidths := []int{45, 60, 10, 10, 12, 12, 12, 12, 8, 8, 6, baseWidths[11], 18}

	headerColor := tcell.ColorYellow
	rowColor := tcell.ColorWhite

	// Recalculate dynamic column widths based on current output text.
	calcColumnWidths := func() []int {
		widths := append([]int(nil), baseWidths...)
		for _, c := range []int{0, 1} {
			maxWidth := runewidth.StringWidth(fullHeaders[c])
			for _, t := range targets {
				view := t.GetView()
				value := ""
				switch c {
				case 0:
					value = displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
				case 1:
					if view.Host != view.IP {
						value = fmt.Sprintf("%s (%s)", view.Host, view.IP)
					} else {
						value = view.IP
					}
				}
				if w := runewidth.StringWidth(value); w > maxWidth {
					maxWidth = w
				}
			}
			widths[c] = maxWidth
		}
		return widths
	}

	widths := calcColumnWidths()
	activeHeaders := append([]string(nil), fullHeaders...)
	activeAligns := append([]int(nil), fullAligns...)
	rowCount := len(targets) + 1
	compactLayout := false

	tablePane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true)
	tablePane.SetBorder(true).SetTitle(" Ping Monitor ").SetBorderColor(tcell.ColorWhite)

	var traceView *tview.TextView
	var tracePane *tview.Flex
	setTraceBorderColor := func(_ tcell.Color) {}

	if traceEnabled {
		traceView = tview.NewTextView().
			SetDynamicColors(true).
			SetScrollable(true).
			SetWrap(false)
		traceView.SetBackgroundColor(tcell.ColorBlack)

		tracePane = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(traceView, 0, 1, true)
		tracePane.SetBorder(true).SetBorderColor(tcell.ColorWhite)

		traceBorderColor := tcell.ColorWhite
		setTraceBorderColor = func(c tcell.Color) {
			traceBorderColor = c
			tracePane.SetBorderColor(c)
		}
		// Override with double-line box drawing characters.
		tracePane.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
			if width < 2 || height < 2 {
				return x + 1, y + 1, width - 2, height - 2
			}
			style := tcell.StyleDefault.Foreground(traceBorderColor)
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
			tview.Print(screen, " Traceroute Monitor ", x+1, y, width-2, tview.AlignCenter, traceBorderColor)
			return x + 1, y + 1, width - 2, height - 2
		})
	}

	// Port Monitor pane
	var portView *tview.TextView
	var portPane *tview.Flex
	setPortBorderColor := func(_ tcell.Color) {}

	if portEnabled {
		portView = tview.NewTextView().
			SetDynamicColors(true).
			SetScrollable(true).
			SetWrap(false)
		portView.SetBackgroundColor(tcell.ColorBlack)

		portPane = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(portView, 0, 1, true)
		portPane.SetBorder(true).SetBorderColor(tcell.ColorWhite)

		portBorderColor := tcell.ColorWhite
		setPortBorderColor = func(c tcell.Color) {
			portBorderColor = c
			portPane.SetBorderColor(c)
		}
		portPane.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
			if width < 2 || height < 2 {
				return x + 1, y + 1, width - 2, height - 2
			}
			style := tcell.StyleDefault.Foreground(portBorderColor)
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
			tview.Print(screen, " Port Monitor ", x+1, y, width-2, tview.AlignCenter, portBorderColor)
			return x + 1, y + 1, width - 2, height - 2
		})
	}

	// Error log state
	errorLogs := []string{}
	lastLossTimes := make(map[string]time.Time)
	alertState := make(map[string]alertFlags)

	for _, line := range initialLogs {
		appendErrorLog(&errorLogs, errorView, line)
	}

	var footer *tview.TextView
	stopRequested := false

	updateTable := func() {
		table.Clear()

		_, _, availableTableWidth, _ := tablePane.GetInnerRect()
		availableColumnsWidth := availableTableWidth - (len(fullHeaders) + 1)
		if availableColumnsWidth < 0 {
			availableColumnsWidth = 0
		}

		updatedWidths := calcColumnWidths()
		dynamicMaxWidths := append([]int(nil), maxWidths...)
		for _, c := range []int{0, 1, 2} {
			if updatedWidths[c] > dynamicMaxWidths[c] {
				dynamicMaxWidths[c] = updatedWidths[c]
			}
		}
		fitted, ok := fitWidthsToAvailable(updatedWidths, minWidths, dynamicMaxWidths, availableColumnsWidth)

		compact := buildCompactLayout(targets, packetSize, sourceIPv4, sourceIPv6, baseWidths[12])
		compactRows := compact.rows
		compactDesired := compact.desired
		compactHeaders := compact.headers
		compactAligns := compact.aligns
		compactMin := compact.min
		compactMax := compact.max
		compactAvailableColumnsWidth := availableTableWidth - (len(compactHeaders) + 1)
		if compactAvailableColumnsWidth < 0 {
			compactAvailableColumnsWidth = 0
		}
		compactWidths, compactOK := fitWidthsToAvailable(compactDesired, compactMin, compactMax, compactAvailableColumnsWidth)

		if ok {
			compactLayout = false
			widths = fitted
			activeHeaders = append([]string(nil), fullHeaders...)
			activeAligns = append([]int(nil), fullAligns...)
			rowCount = len(targets) + 1
		} else if compactOK {
			compactLayout = true
			widths = compactWidths
			activeHeaders = append([]string(nil), compactHeaders...)
			activeAligns = append([]int(nil), compactAligns...)
			rowCount = len(compactRows) + 1
		}

		// Header
		for i, h := range activeHeaders {
			text := formatCellText(h, widths[i], activeAligns[i])
			cell := tview.NewTableCell(text).
				SetBackgroundColor(tcell.ColorBlack).
				SetTextColor(headerColor).
				SetAttributes(tcell.AttrBold).
				SetSelectable(false).
				SetAlign(activeAligns[i])
			if i == len(activeHeaders)-1 {
				cell.SetExpansion(1)
			}
			table.SetCell(0, i, cell)
		}

		pickCompact := func(right, left string) string {
			if right != "" {
				return right
			}
			return left
		}
		if compactLayout {
			for i, r := range compactRows {
				row := i + 1
				values := []string{
					pickCompact(r.hostR, r.hostL),
					pickCompact(r.pathR, r.pathL),
					pickCompact(r.statR, r.statL),
					pickCompact(r.errR, r.errL),
				}
				cells := buildCompactRowCells(values, widths, activeAligns, vividRed, rowColor)
				for c, cell := range cells {
					table.SetCell(row, c, cell)
				}
			}
		}

		// Update table rows AND Error logs
		for i, t := range targets {
			view := t.GetView()

			// Check for new errors
			if !view.LastLossTime.IsZero() {
				lastTime, exists := lastLossTimes[view.Host]
				if !exists || view.LastLossTime.After(lastTime) {
					// New error detected
					lastLossTimes[view.Host] = view.LastLossTime
					rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
					msg := buildErrorLogMessage(view, rowSourceIP, view.LastError, view.LastLossTime)
					appendErrorLog(&errorLogs, errorView, msg)
				}
			}

			if compactLayout {
				continue
			}

			row := i + 1
			cols, rowSourceIP, lossRate := buildFullColumns(view, sourceIPv4, sourceIPv6, packetSize)

			// Alert logs on red thresholds
			state := alertState[view.Host]
			now := time.Now()
			state, msgs := updateAlertState(view, rowSourceIP, lossRate, now, state)
			for _, msg := range msgs {
				appendErrorLog(&errorLogs, errorView, msg)
			}
			alertState[view.Host] = state

			cells := buildFullRowCells(cols, widths, fullAligns, lossRate, view.LastRTT, view.Jitter, vividRed, rowColor)
			for c, cell := range cells {
				table.SetCell(row, c, cell)
			}

		}

		if traceEnabled && traceView != nil {
			// Compute Host column width from the longest hostname (with 1-space padding each side).
			hostColW := runewidth.StringWidth("Host")
			for _, t := range targets {
				if w := runewidth.StringWidth(t.GetView().Host); w > hostColW {
					hostColW = w
				}
			}
			hostColW += 2 // 1 space left + 1 space right

			// Compute Route column width from the longest route string (with 1-space padding each side).
			routeColW := runewidth.StringWidth("Route")
			for _, t := range targets {
				hops := t.GetView().TraceHops
				if w := runewidth.StringWidth(strings.Join(hops, " -> ")); w > routeColW {
					routeColW = w
				}
			}
			routeColW += 2 // 1 space left + 1 space right

			// Expand Route column to fill available terminal width.
			// Available width = inner rect width - 3 border chars (│host│route│).
			_, _, availW, _ := traceView.GetInnerRect()
			if expanded := availW - hostColW - 3; expanded > routeColW {
				routeColW = expanded
			}

			// cell returns text with a leading space, right-padded to fill colW.
			cell := func(text string, colW int) string {
				return formatCellText(" "+text, colW, tview.AlignLeft)
			}

			h := strings.Repeat("─", hostColW)
			r := strings.Repeat("─", routeColW)

			var sb strings.Builder

			// Top border.
			fmt.Fprintf(&sb, "[white]┌%s┬%s┐[-]\n", h, r)

			// Header row: yellow bold labels, darkgray borders.
			fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
				cell("Host", hostColW), cell("Route", routeColW))

			// Header separator.
			fmt.Fprintf(&sb, "[white]├%s┼%s┤[-]\n", h, r)

			// Data rows: host in cyan, route in white, darkgray borders, separator between rows.
			dataTargets := make([]*stats.TargetStats, 0, len(targets))
			for _, t := range targets {
				if len(t.GetView().TraceHops) > 0 {
					dataTargets = append(dataTargets, t)
				}
			}
			for i, t := range dataTargets {
				view := t.GetView()
				route := strings.Join(view.TraceHops, " -> ")
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					cell(view.Host, hostColW), cell(route, routeColW))
				if i < len(dataTargets)-1 {
					fmt.Fprintf(&sb, "[white]├%s┼%s┤[-]\n", h, r)
				}
			}

			// Bottom border.
			fmt.Fprintf(&sb, "[white]└%s┴%s┘[-]\n", h, r)

			traceView.SetText(sb.String())
		}

		if portEnabled && portView != nil {
			_, _, availW, _ := portView.GetInnerRect()

			// Column widths: Target, Port, Status, Open/Closed, Last Change
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
			countColW := runewidth.StringWidth("Open/Closed") + 2
			// Expand Last Change column to fill remaining width
			changeColW := runewidth.StringWidth("Last Change") + 2
			used := targetColW + portColW + serviceColW + statusColW + countColW + changeColW + 6
			if availW > used {
				changeColW += availW - used
			}

			cell := func(text string, colW int) string {
				return formatCellText(" "+text, colW, tview.AlignLeft)
			}
			th := strings.Repeat("─", targetColW)
			ph := strings.Repeat("─", portColW)
			svh := strings.Repeat("─", serviceColW)
			sh := strings.Repeat("─", statusColW)
			ch := strings.Repeat("─", countColW)
			lh := strings.Repeat("─", changeColW)

			var sb strings.Builder
			fmt.Fprintf(&sb, "[white]┌%s┬%s┬%s┬%s┬%s┬%s┐[-]\n", th, ph, svh, sh, ch, lh)
			fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
				cell("Target", targetColW), cell("Port", portColW), cell("Service", serviceColW),
				cell("Status", statusColW), cell("Open/Closed", countColW), cell("Last Change", changeColW))
			fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┤[-]\n", th, ph, svh, sh, ch, lh)

			// Collect targets that have results for separator logic
			dataTargets := make([]*stats.TargetStats, 0, len(targets))
			for _, t := range targets {
				if len(t.GetView().PortResults) > 0 {
					dataTargets = append(dataTargets, t)
				}
			}

			rowCount := 0
			for ti, t := range dataTargets {
				view := t.GetView()
				for i, pr := range view.PortResults {
					statusColor := "[white]"
					switch pr.Status {
					case "Open":
						statusColor = "[green]"
					case "Closed":
						statusColor = "[red]"
					case "Filtered", "Open|Filtered":
						statusColor = "[yellow]"
					}
					countStr := fmt.Sprintf("%d/%d", pr.OpenCount, pr.ClosedCount)
					changeStr := "-"
					if !pr.LastChange.IsZero() {
						changeStr = formatLossAgo(pr.LastChange)
					}
					targetName := ""
					if i == 0 {
						targetName = view.Host
					}
					fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[-]\n",
						cell(targetName, targetColW),
						cell(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol), portColW),
						cell(portServiceName(pr.Port, pr.Protocol), serviceColW),
						statusColor, cell(pr.Status, statusColW),
						cell(countStr, countColW),
						cell(changeStr, changeColW))
					rowCount++
				}
				if ti < len(dataTargets)-1 {
					fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┤[-]\n", th, ph, svh, sh, ch, lh)
				}
			}
			if rowCount == 0 {
				total := targetColW + portColW + serviceColW + statusColW + countColW + changeColW + 5
				fmt.Fprintf(&sb, "[white]│[darkgray]%s[white]│[-]\n",
					formatCellText(" Waiting for results...", total, tview.AlignLeft))
			}
			fmt.Fprintf(&sb, "[white]└%s┴%s┴%s┴%s┴%s┴%s┘[-]\n", th, ph, svh, sh, ch, lh)

			portView.SetText(sb.String())
		}
	}

	// Keys
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if app.GetFocus() == table {
			switch event.Key() {
			case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
				rowOffset, colOffset := table.GetOffset()
				totalRows := rowCount
				visibleRows := tableMaxRows + 1
				maxOffset := totalRows - visibleRows
				if maxOffset < 0 {
					maxOffset = 0
				}

				delta := 0
				switch event.Key() {
				case tcell.KeyUp:
					delta = -1
				case tcell.KeyDown:
					delta = 1
				case tcell.KeyPgUp:
					delta = -tableMaxRows
				case tcell.KeyPgDn:
					delta = tableMaxRows
				}

				rowOffset += delta
				if rowOffset < 0 {
					rowOffset = 0
				} else if rowOffset > maxOffset {
					rowOffset = maxOffset
				}

				table.SetOffset(rowOffset, colOffset)
				return nil
			}
		}
		switch event.Key() {
		case tcell.KeyTab:
			resetAll := func() {
				table.SetBorderColor(tcell.ColorWhite)
				errorView.SetBorderColor(tcell.ColorRed)
				graphView.SetBorderColor(vividCyan)
				setTraceBorderColor(tcell.ColorWhite)
				setPortBorderColor(tcell.ColorWhite)
			}
			if app.GetFocus() == table {
				if traceEnabled && traceView != nil {
					resetAll()
					app.SetFocus(traceView)
					setTraceBorderColor(tcell.ColorGreen)
				} else if portEnabled && portView != nil {
					resetAll()
					app.SetFocus(portView)
					setPortBorderColor(tcell.ColorGreen)
				} else {
					resetAll()
					app.SetFocus(graphView)
					graphView.SetBorderColor(tcell.ColorGreen)
				}
			} else if traceEnabled && traceView != nil && app.GetFocus() == traceView {
				if portEnabled && portView != nil {
					resetAll()
					app.SetFocus(portView)
					setPortBorderColor(tcell.ColorGreen)
				} else {
					resetAll()
					app.SetFocus(graphView)
					graphView.SetBorderColor(tcell.ColorGreen)
				}
			} else if portEnabled && portView != nil && app.GetFocus() == portView {
				resetAll()
				app.SetFocus(graphView)
				graphView.SetBorderColor(tcell.ColorGreen)
			} else if app.GetFocus() == graphView {
				resetAll()
				app.SetFocus(errorView)
				errorView.SetBorderColor(tcell.ColorGreen)
			} else {
				resetAll()
				app.SetFocus(table)
				table.SetBorderColor(tcell.ColorGreen)
			}
			return nil
		}

		switch event.Rune() {
		case 'q':
			app.Stop()
		case 's':
			if !stopRequested {
				stopRequested = true
				appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[yellow][%s] Stop requested by user[-]", time.Now().Format("15:04:05")))
				if onStop != nil {
					go onStop()
				}
				if footer != nil {
					footer.SetText("Stopped. Press 'S' to restart, 'q' to quit, 'R' to reset stats")
					footer.SetTextColor(tcell.ColorYellow)
				}
			}
		case 'S':
			if stopRequested {
				appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[yellow][%s] Restart requested by user[-]", time.Now().Format("15:04:05")))
				if onRestart != nil {
					go func() {
						if err := onRestart(); err != nil {
							app.QueueUpdateDraw(func() {
								appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[red][%s] Restart failed: %v[-]", time.Now().Format("15:04:05"), err))
							})
							return
						}
						app.QueueUpdateDraw(func() {
							stopRequested = false
							if footer != nil {
								footer.SetText("Tab: Switch Focus | q: Quit | s: Stop ping | R: Reset stats")
								footer.SetTextColor(tcell.ColorYellow)
							}
						})
					}()
				}
			}
		case 'R':
			for _, t := range targets {
				t.Reset()
			}
			// Also clear error log
			errorLogs = []string{}
			errorView.SetText("")
			lastLossTimes = make(map[string]time.Time)
			alertState = make(map[string]alertFlags)
		}
		return event
	})

	header := tview.NewTextView().
		SetText(fmt.Sprintf("MPING - Multi Ping Tool | Interval: %dms", interval.Milliseconds())).
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorGreen).
		SetWrap(false)
	header.SetBackgroundColor(tcell.ColorBlack)

	footer = tview.NewTextView().
		SetText("Tab: Switch Focus | q: Quit | s: Stop ping | R: Reset stats").
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorYellow).
		SetWrap(false)
	footer.SetBackgroundColor(tcell.ColorBlack)

	// Refresh loop
	go func() {
		ticker := time.NewTicker(interval / 2)
		if interval < minUIRefresh {
			ticker.Reset(fastUIRefresh)
		}
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				app.QueueUpdateDraw(updateTable)
			case <-doneCh:
				// Pinger finished (count limit reached)
				app.QueueUpdateDraw(func() {
					footer.SetText("Finished. Press 'q' to quit, 'R' to reset stats")
					footer.SetTextColor(tcell.ColorGreen)
					updateTable()
				})
				// Stop refreshing since pinger is done
				return
			}
		}
	}()

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 2, 0, false).
		AddItem(tablePane, 0, 3, true).  // Table: 3 parts
		AddItem(graphView, 0, 3, false). // Graph: 3 parts
		AddItem(errorView, 0, 2, false). // Logs: 2 parts
		AddItem(footer, 2, 0, false)

	if traceEnabled && tracePane != nil && portEnabled && portPane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(tracePane, 0, 2, false). // Trace: 2 parts
			AddItem(portPane, 0, 2, false).  // Port: 2 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(footer, 2, 0, false)
	} else if traceEnabled && tracePane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(tracePane, 0, 3, false). // Trace: 3 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(footer, 2, 0, false)
	} else if portEnabled && portPane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(portPane, 0, 3, false).  // Port: 3 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(footer, 2, 0, false)
	}

	flex.SetBackgroundColor(tcell.ColorBlack)

	return app.SetRoot(flex, true).Run()
}
