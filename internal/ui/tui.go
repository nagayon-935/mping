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
	tableMaxRows        = 10
	// Table rows include header; with table borders enabled, each row consumes 2 lines plus 1 for bottom border.
	tablePaneHeight = ((tableMaxRows + 1) * 2) + 1 + 2 // table height + pane border
	traceMaxRows    = 10
	tracePaneHeight = ((traceMaxRows + 1) * 2) + 1 + 2 // table height + pane border
	traceHostWidth  = 20

	rttOrangeThreshold    = 50 * time.Millisecond
	rttRedThreshold       = 200 * time.Millisecond
	jitterOrangeThreshold = 10 * time.Millisecond
	jitterRedThreshold    = 50 * time.Millisecond
	lossRedThreshold      = 80.0
)

var graphGridValues = []int{25, 50, 75, 100}

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
	sourceIP  string // for reference if needed, though mostly for table
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

	shrinkOrder := []int{12, 2, 1, 0, 13, 8, 7, 6, 5, 3, 4, 10, 11, 9}
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

	growOrder := []int{0, 2, 1, 12, 13, 3, 6, 7, 8, 5, 4, 9, 10, 11}
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

func appendErrorLog(errorLogs *[]string, errorView *tview.TextView, msg string) {
	*errorLogs = append(*errorLogs, msg)
	if len(*errorLogs) > 1000 {
		*errorLogs = (*errorLogs)[1:]
	}
	errorText := ""
	for _, log := range *errorLogs {
		errorText += log + "\n"
	}
	errorView.SetText(errorText)
	errorView.ScrollToEnd()
}

func wrapRoute(hops []string, maxWidth int) string {
	if maxWidth <= 0 || len(hops) == 0 {
		return "-"
	}
	var lines []string
	current := ""
	sep := " -> "
	for _, hop := range hops {
		if current == "" {
			current = hop
			continue
		}
		next := current + sep + hop
		if len([]rune(next)) <= maxWidth {
			current = next
			continue
		}
		lines = append(lines, current)
		current = hop
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

func wrapRouteLines(hops []string, maxWidth int) []string {
	if maxWidth <= 0 || len(hops) == 0 {
		return []string{"-"}
	}
	var lines []string
	current := ""
	sep := " -> "
	for _, hop := range hops {
		if current == "" {
			current = hop
			continue
		}
		next := current + sep + hop
		if len([]rune(next)) <= maxWidth {
			current = next
			continue
		}
		lines = append(lines, current)
		current = hop
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
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

		_, _, _, height := g.GetInnerRect()
		if height <= 0 {
			return
		}

		numTargets := len(g.targets)
		if numTargets == 0 {
			g.scrollRow = 0
			return
		}

		numCols := 1
		if numTargets > 1 {
			numCols = 2
		}
		numRowsTotal := (numTargets + numCols - 1) / numCols
		visibleRows := numRowsTotal
		if visibleRows > graphMaxVisibleRows {
			visibleRows = graphMaxVisibleRows
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

	// Determine layout (1 col or 2 cols)
	numCols := 1
	if numTargets > 1 {
		numCols = 2
	}

	numRowsTotal := (numTargets + numCols - 1) / numCols
	visibleRows := numRowsTotal
	if visibleRows > graphMaxVisibleRows {
		visibleRows = graphMaxVisibleRows
	}
	g.clampScroll(numRowsTotal, visibleRows)

	rowHeight := height / visibleRows
	if rowHeight < 2 {
		rowHeight = 2 // Minimum height per target block
	}

	colWidth := width / numCols

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

			// Y-Axis fixed to 0-100ms
			const yMax = 100 * time.Millisecond
			const yMin = 0
			const yMaxMs = 100.0

			desiredStep := 4
			desiredHeight := (desiredStep * 4) + 1 // 0-25-50-75-100ms with 4 rows each
			plotHeight := graphHeight
			plotY := graphY
			if graphHeight >= desiredHeight {
				plotHeight = desiredHeight
				plotY = graphY + (graphHeight - plotHeight)
			}

			rangeVal := float64(yMax - yMin)

			// Draw Grid Lines (25, 50, 75 ms)
			gridYPos := make(map[int]bool)

			totalSteps := plotHeight - 1
			if totalSteps < 1 {
				totalSteps = 1
			}
			gy25, gy50, gy75, gy100 := 0, 0, 0, totalSteps
			if plotHeight == desiredHeight {
				gy25 = desiredStep - 1
				gy50 = gy25 + desiredStep
				gy75 = gy50 + desiredStep
				gy100 = gy75 + desiredStep
			} else {
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
			}

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
						screen.SetContent(graphX+gx, py, '·', nil, tcell.StyleDefault.Foreground(tcell.ColorDarkGray))
					}
					// Draw label
					tview.Print(screen, fmt.Sprintf("%dms", val), graphX+graphWidth+1, py, labelWidth, tview.AlignLeft, tcell.ColorDarkGray)
				}
			}

			// Label for 0ms (Bottom)
			bottomY := plotY + plotHeight - 1
			tview.Print(screen, "0ms", graphX+graphWidth+1, bottomY, labelWidth, tview.AlignLeft, tcell.ColorDarkGray)

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
								color = tcell.ColorDarkGray
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
					screen.SetContent(baseX+sx, sepY, '─', nil, tcell.StyleDefault.Foreground(tcell.ColorDarkGray))
				}
			}

		}
	}
}

func Run(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, onStop func(), onRestart func() error) error {
	// Define vivid colors
	vividRed := tcell.NewRGBColor(255, 0, 0)
	vividCyan := tcell.NewRGBColor(0, 255, 255)

	app := tview.NewApplication()
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

	// Set black background and white borders
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetBorderColor(tcell.ColorWhite)

	// Columns: Hostname, Src IP, Dst IP, Success, Loss, Loss Ratio, RTT, Avg, Jitter, Size, MTU, TTL, Error, Last Loss
	fullHeaders := []string{"Hostname", "Src IP", "Dst IP", "Success", "Loss", "Loss Ratio", "RTT", "Avg", "Jitter", "Size", "MTU", "TTL", "Error", "Last Loss"}
	fullAligns := []int{
		tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignRight, tview.AlignRight, tview.AlignRight,
		tview.AlignRight, tview.AlignRight, tview.AlignRight, // RTTs
		tview.AlignRight, tview.AlignRight, tview.AlignRight, tview.AlignLeft, tview.AlignLeft,
	}
	// Hostname / Src IP / Dst IP are dynamically resized from the rendered content.
	// Error width is fixed at startup to prevent table size jumps when new errors arrive.
	baseWidths := []int{8, 6, 6, 8, 7, 10, 10, 10, 10, 6, 6, 5, 30, 15}
	baseWidths[12] = calcInitialTableErrorWidth(targets, fullHeaders[12], baseWidths[12])
	minWidths := []int{8, 4, 8, 5, 4, 6, 7, 7, 7, 4, 4, 3, 8, 8}
	maxWidths := []int{40, 45, 45, 10, 10, 12, 12, 12, 12, 8, 8, 6, baseWidths[12], 18}

	headerColor := tcell.ColorYellow
	rowColor := tcell.ColorWhite

	calcTableWidth := func(widths []int) int {
		total := 0
		for _, w := range widths {
			total += w
		}
		return total + len(widths) + 1
	}

	// Recalculate dynamic column widths based on current output text.
	calcColumnWidths := func() []int {
		widths := append([]int(nil), baseWidths...)
		for _, c := range []int{0, 1, 2} {
			maxWidth := runewidth.StringWidth(fullHeaders[c])
			for _, t := range targets {
				view := t.GetView()
				value := ""
				switch c {
				case 0:
					value = view.Host
				case 1:
					value = displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
				case 2:
					value = view.IP
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
	tableWidth := calcTableWidth(widths)
	activeHeaders := append([]string(nil), fullHeaders...)
	activeAligns := append([]int(nil), fullAligns...)
	rowCount := len(targets) + 1
	compactLayout := false

	// tableContainer centers the table horizontally
	tableContainer := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, 0, 1, false).
		AddItem(table, tableWidth, 0, true).
		AddItem(nil, 0, 1, false)

	tablePane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tableContainer, 0, 1, true)
	tablePane.SetBorder(true).SetTitle(" Table ").SetBorderColor(tcell.ColorWhite)

	var traceTable *tview.Table
	var traceContainer *tview.Flex
	var tracePane *tview.Flex
	traceRouteWidth := 0
	traceRowCount := 1
	{
		traceRouteWidth = tableWidth - traceHostWidth - 3
		if traceRouteWidth < 10 {
			traceRouteWidth = 10
		}
	}

	if traceEnabled {
		traceTable = tview.NewTable().
			SetBorders(true).
			SetSelectable(false, false).
			SetFixed(1, 1)
		traceTable.SetBackgroundColor(tcell.ColorBlack)
		traceTable.SetBorderColor(tcell.ColorWhite)

		// traceContainer centers the trace table horizontally
		traceContainer = tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(nil, 0, 1, false).
			AddItem(traceTable, tableWidth, 0, true).
			AddItem(nil, 0, 1, false)

		tracePane = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(traceContainer, 0, 1, true)
		tracePane.SetBorder(true).SetTitle(" Traceroute ").SetBorderColor(tcell.ColorWhite)
	}

	// Error log state
	errorLogs := []string{}
	lastLossTimes := make(map[string]time.Time)
	alertState := make(map[string]struct {
		lossRed   bool
		rttRed    bool
		jitterRed bool
	})

	for _, line := range initialLogs {
		appendErrorLog(&errorLogs, errorView, line)
	}

	var footer *tview.TextView
	stopRequested := false

	updateTable := func() {
		table.Clear()

		_, _, availableTableWidth, _ := tablePane.GetInnerRect()
		if availableTableWidth <= 0 {
			availableTableWidth = tableWidth
		}
		availableColumnsWidth := availableTableWidth - (len(fullHeaders) + 1)
		if availableColumnsWidth < 0 {
			availableColumnsWidth = 0
		}

		updatedWidths := calcColumnWidths()
		fitted, ok := fitWidthsToAvailable(updatedWidths, minWidths, maxWidths, availableColumnsWidth)

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
		compactHeaders := []string{"Host", "Path", "Stats", "Error"}
		compactAligns := []int{tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignLeft}
		compactRows := make([]compactRow, 0, len(targets)*2)
		compactDesired := []int{
			runewidth.StringWidth(compactHeaders[0]),
			runewidth.StringWidth(compactHeaders[1]),
			runewidth.StringWidth(compactHeaders[2]),
			runewidth.StringWidth(compactHeaders[3]),
		}
		compactMin := []int{8, 16, 18, 8}
		compactMax := []int{40, 80, 80, baseWidths[12]}

		for _, t := range targets {
			view := t.GetView()
			lossRate := calcLossRate(view)
			lossStr := formatLossAgo(view.LastLossTime)
			rttStr := formatRTT(view.LastRTT)
			avgStr := formatRTT(view.AvgRTT)
			jitterStr := formatRTT(view.Jitter)
			mtuStr := "-"
			if view.IfaceMTU > 0 {
				mtuStr = fmt.Sprintf("%d", view.IfaceMTU)
			}
			ttlStr := "-"
			if view.LastTTL > 0 {
				ttlStr = fmt.Sprintf("%d", view.LastTTL)
			}
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
			compactRows = append(compactRows, r1, r2)

			for i, v := range []string{r1.hostL, r1.pathL, r1.statL, r1.errL, r2.hostR, r2.pathR, r2.statR, r2.errR} {
				col := i % 4
				if w := runewidth.StringWidth(v); w > compactDesired[col] {
					compactDesired[col] = w
				}
			}
		}

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

		newTableWidth := calcTableWidth(widths)
		if newTableWidth != tableWidth {
			tableWidth = newTableWidth
			tableContainer.ResizeItem(table, tableWidth, 0)
			if traceEnabled && traceContainer != nil && traceTable != nil {
				traceContainer.ResizeItem(traceTable, tableWidth, 0)
				traceRouteWidth = tableWidth - traceHostWidth - 3
				if traceRouteWidth < 10 {
					traceRouteWidth = 10
				}
			}
		}

		// Header
		for i, h := range activeHeaders {
			text := formatCellText(h, widths[i], activeAligns[i])
			table.SetCell(0, i, tview.NewTableCell(text).
				SetBackgroundColor(tcell.ColorBlack).
				SetTextColor(headerColor).
				SetAttributes(tcell.AttrBold).
				SetSelectable(false).
				SetAlign(activeAligns[i]))
		}

		if compactLayout {
			for i, r := range compactRows {
				row := i + 1
				values := []string{r.hostL + r.hostR, r.pathL + r.pathR, r.statL + r.statR, r.errL + r.errR}
				if r.hostR != "" {
					values[0] = r.hostR
				} else {
					values[0] = r.hostL
				}
				if r.pathR != "" {
					values[1] = r.pathR
				} else {
					values[1] = r.pathL
				}
				if r.statR != "" {
					values[2] = r.statR
				} else {
					values[2] = r.statL
				}
				if r.errR != "" {
					values[3] = r.errR
				} else {
					values[3] = r.errL
				}
				for c, v := range values {
					cell := tview.NewTableCell(formatCellText(v, widths[c], activeAligns[c])).
						SetBackgroundColor(tcell.ColorBlack).
						SetTextColor(rowColor).
						SetAlign(activeAligns[c])
					if c == 3 && strings.TrimSpace(v) != "" {
						cell.SetTextColor(vividRed)
					}
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
					timestamp := view.LastLossTime.Format("15:04:05")
					errMsg := view.LastError
					rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
					if rowSourceIP != "" && rowSourceIP != "Auto" && strings.Contains(errMsg, "write ip 0.0.0.0->") {
						errMsg = strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+rowSourceIP+"->", 1)
					}
					msg := fmt.Sprintf("[red][%s] %s (%s): %s[-]", timestamp, view.Host, rowSourceIP, errMsg)
					appendErrorLog(&errorLogs, errorView, msg)
				}
			}

			if compactLayout {
				continue
			}

			row := i + 1
			lossRate := calcLossRate(view)
			lossStr := formatLossAgo(view.LastLossTime)

			rttStr := formatRTT(view.LastRTT)
			avgStr := formatRTT(view.AvgRTT)
			jitterStr := formatRTT(view.Jitter)

			ttlStr := "-"
			if view.LastTTL > 0 {
				ttlStr = fmt.Sprintf("%d", view.LastTTL)
			}

			lossColor := tcell.ColorGreen
			if lossRate > 20 {
				lossColor = tcell.ColorOrange
			}
			if lossRate > lossRedThreshold {
				lossColor = vividRed
			}

			mtuStr := "-"
			if view.IfaceMTU > 0 {
				mtuStr = fmt.Sprintf("%d", view.IfaceMTU)
			}

			rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)

			cols := []string{
				view.Host,
				rowSourceIP,
				view.IP, // Dst IP
				fmt.Sprintf("%d", view.Recv),
				fmt.Sprintf("%d", view.Loss),
				fmt.Sprintf("%.1f%%", lossRate),
				rttStr,
				avgStr,
				jitterStr,
				fmt.Sprintf("%d", packetSize),
				mtuStr,
				ttlStr,
				formatTableError(view.LastError),
				lossStr,
			}

			rttColor := rowColor
			if view.LastRTT > 0 {
				if view.LastRTT > rttRedThreshold {
					rttColor = vividRed
				} else if view.LastRTT > rttOrangeThreshold {
					rttColor = tcell.ColorOrange
				} else {
					rttColor = tcell.ColorGreen
				}
			}

			jitterColor := rowColor
			if view.Jitter > 0 {
				if view.Jitter > jitterRedThreshold {
					jitterColor = vividRed
				} else if view.Jitter > jitterOrangeThreshold {
					jitterColor = tcell.ColorOrange
				} else {
					jitterColor = tcell.ColorGreen
				}
			}

			// Alert logs on red thresholds
			state := alertState[view.Host]
			now := time.Now()
			if lossRate > lossRedThreshold {
				if !state.lossRed {
					msg := fmt.Sprintf("[red][%s] %s (%s): Loss Ratio %.1f%%[-]", now.Format("15:04:05"), view.Host, rowSourceIP, lossRate)
					appendErrorLog(&errorLogs, errorView, msg)
				}
				state.lossRed = true
			} else {
				state.lossRed = false
			}

			if view.LastRTT > rttRedThreshold {
				if !state.rttRed {
					msg := fmt.Sprintf("[red][%s] %s (%s): RTT %v[-]", now.Format("15:04:05"), view.Host, rowSourceIP, view.LastRTT.Round(time.Microsecond))
					appendErrorLog(&errorLogs, errorView, msg)
				}
				state.rttRed = true
			} else {
				state.rttRed = false
			}

			if view.Jitter > jitterRedThreshold {
				if !state.jitterRed {
					msg := fmt.Sprintf("[red][%s] %s (%s): Jitter %v[-]", now.Format("15:04:05"), view.Host, rowSourceIP, view.Jitter.Round(time.Microsecond))
					appendErrorLog(&errorLogs, errorView, msg)
				}
				state.jitterRed = true
			} else {
				state.jitterRed = false
			}
			alertState[view.Host] = state

			for c, col := range cols {
				text := formatCellText(col, widths[c], fullAligns[c])
				cell := tview.NewTableCell(text).
					SetBackgroundColor(tcell.ColorBlack).
					SetTextColor(rowColor).
					SetAlign(fullAligns[c])

				if c == 5 { // Loss Ratio column index
					cell.SetTextColor(lossColor).SetAttributes(tcell.AttrBold)
				}
				if c == 6 { // RTT column index
					cell.SetTextColor(rttColor)
				}
				if c == 8 { // Jitter column index
					cell.SetTextColor(jitterColor)
				}
				if c == 12 && text != "" { // Error column
					cell.SetTextColor(vividRed)
				}

				table.SetCell(row, c, cell)
			}

			if traceEnabled && traceTable != nil {
				if row == 1 {
					traceTable.SetCell(0, 0, tview.NewTableCell("Host").
						SetBackgroundColor(tcell.ColorBlack).
						SetTextColor(headerColor).
						SetAttributes(tcell.AttrBold).
						SetSelectable(false).
						SetAlign(tview.AlignLeft).
						SetMaxWidth(traceHostWidth))
					traceTable.SetCell(0, 1, tview.NewTableCell("Route").
						SetBackgroundColor(tcell.ColorBlack).
						SetTextColor(headerColor).
						SetAttributes(tcell.AttrBold).
						SetSelectable(false).
						SetAlign(tview.AlignLeft).
						SetMaxWidth(traceRouteWidth))
				}
			}
		}

		if traceEnabled && traceTable != nil {
			traceRow := 1
			for _, t := range targets {
				view := t.GetView()
				lines := wrapRouteLines(view.TraceHops, traceRouteWidth)
				for i, line := range lines {
					hostText := ""
					if i == 0 {
						hostText = view.Host
					}
					traceTable.SetCell(traceRow, 0, tview.NewTableCell(hostText).
						SetBackgroundColor(tcell.ColorBlack).
						SetTextColor(rowColor).
						SetAlign(tview.AlignLeft).
						SetMaxWidth(traceHostWidth))
					traceTable.SetCell(traceRow, 1, tview.NewTableCell(line).
						SetBackgroundColor(tcell.ColorBlack).
						SetTextColor(rowColor).
						SetAlign(tview.AlignLeft).
						SetMaxWidth(traceRouteWidth))
					traceRow++
				}
			}
			traceRowCount = traceRow
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
		if traceEnabled && traceTable != nil && app.GetFocus() == traceTable {
			switch event.Key() {
			case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
				rowOffset, colOffset := traceTable.GetOffset()
				totalRows := traceRowCount
				visibleRows := traceMaxRows + 1
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
					delta = -traceMaxRows
				case tcell.KeyPgDn:
					delta = traceMaxRows
				}
				rowOffset += delta
				if rowOffset < 0 {
					rowOffset = 0
				} else if rowOffset > maxOffset {
					rowOffset = maxOffset
				}
				traceTable.SetOffset(rowOffset, colOffset)
				return nil
			}
		}
		switch event.Key() {
		case tcell.KeyTab:
			if app.GetFocus() == table {
				if traceEnabled && traceTable != nil {
					app.SetFocus(traceTable)
					tracePane.SetBorderColor(tcell.ColorGreen)
					table.SetBorderColor(tcell.ColorWhite)
					errorView.SetBorderColor(tcell.ColorRed)
					graphView.SetBorderColor(vividCyan)
				} else {
					app.SetFocus(graphView)
					graphView.SetBorderColor(tcell.ColorGreen)
					table.SetBorderColor(tcell.ColorWhite)
					errorView.SetBorderColor(tcell.ColorRed)
				}
			} else if traceEnabled && traceTable != nil && app.GetFocus() == traceTable {
				app.SetFocus(graphView)
				graphView.SetBorderColor(tcell.ColorGreen)
				if tracePane != nil {
					tracePane.SetBorderColor(tcell.ColorWhite)
				}
				table.SetBorderColor(tcell.ColorWhite)
				errorView.SetBorderColor(tcell.ColorRed)
			} else if app.GetFocus() == graphView {
				app.SetFocus(errorView)
				errorView.SetBorderColor(tcell.ColorGreen)
				graphView.SetBorderColor(vividCyan)
				table.SetBorderColor(tcell.ColorWhite)
			} else {
				app.SetFocus(table)
				table.SetBorderColor(tcell.ColorGreen)
				errorView.SetBorderColor(tcell.ColorRed)
				graphView.SetBorderColor(vividCyan)
				if tracePane != nil {
					tracePane.SetBorderColor(tcell.ColorWhite)
				}
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
			alertState = make(map[string]struct {
				lossRed   bool
				rttRed    bool
				jitterRed bool
			})
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
		if interval < 200*time.Millisecond {
			ticker.Reset(100 * time.Millisecond)
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
		AddItem(tablePane, tablePaneHeight, 0, true).
		AddItem(graphView, 0, 4, false). // Graph: 40%
		AddItem(errorView, errorLogBoxHeight, 0, false).
		AddItem(footer, 2, 0, false)

	if traceEnabled && tracePane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, tablePaneHeight, 0, true).
			AddItem(tracePane, tracePaneHeight, 0, false).
			AddItem(graphView, 0, 4, false).
			AddItem(errorView, errorLogBoxHeight, 0, false).
			AddItem(footer, 2, 0, false)
	}

	flex.SetBackgroundColor(tcell.ColorBlack)

	return app.SetRoot(flex, true).Run()
}
