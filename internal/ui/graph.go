package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

const (
	graphMaxVisibleRows = 3
	graphLabelWidth     = 7 // e.g. "1000ms"
	graphMinWidth       = 10
	graphWindowSeconds  = 30
	graphScaleFloorMs   = 100.0 // minimum Y-axis upper bound in ms
	graphScaleHoldSecs  = 5     // seconds before scale is allowed to shrink
)

// graphSeries abstracts a single RTT data series for the graph.
// Both ICMP targets and TCP/UDP port checks implement this interface.
type graphSeries interface {
	seriesLabel() string
	seriesSnapshot() (history []time.Duration, lastRTT time.Duration)
}

// icmpSeries holds a pre-fetched snapshot rather than a live
// *stats.TargetStats reference: buildSeries takes exactly one
// GetViewWindow() call per target, and seriesLabel/seriesSnapshot below
// just read the already-fetched fields instead of each re-fetching from
// TargetStats (previously 3 separate GetView() calls per target per Draw —
// one for the label, one directly, one via windowMax's own snapshot call).
type icmpSeries struct {
	label   string
	history []time.Duration
	lastRTT time.Duration
}

func (s icmpSeries) seriesLabel() string { return s.label }
func (s icmpSeries) seriesSnapshot() ([]time.Duration, time.Duration) {
	return s.history, s.lastRTT
}

// GraphView is a custom primitive for rendering RTT graphs
type GraphView struct {
	*tview.Box
	targets      []*stats.TargetStats
	interval     time.Duration
	vividCyan    tcell.Color
	vividRed     tcell.Color
	scrollRow    int
	includePorts bool

	// Auto-scale state. Accessed from Draw only, which — like the rest of
	// the ui package's mutable render state (see viewState, tableRenderer)
	// — runs exclusively on tview's single draw/event-loop goroutine. No
	// additional lock is needed, and none may be assumed safe if this state
	// is ever touched from elsewhere.
	currentScale   yScale
	scaleHoldUntil time.Time
}

func NewGraphView(targets []*stats.TargetStats, interval time.Duration) *GraphView {
	return &GraphView{
		Box:          tview.NewBox(),
		targets:      targets,
		interval:     interval,
		vividCyan:    tcell.NewRGBColor(0, 255, 255),
		vividRed:     tcell.NewRGBColor(255, 0, 0),
		currentScale: computeYScale(0, graphScaleFloorMs),
	}
}

// buildSeries constructs the list of graphSeries from current target state,
// fetching exactly one GetViewWindow(historyWindow) snapshot per target.
// Called at the start of each Draw so that dynamically added targets
// appear. historyWindow should be the same trailing-window size Draw is
// about to plot (timeBasedWidth) — GetViewWindow already truncates to it,
// so windowMax/projectDurationsToGraph's own truncation below becomes a
// no-op rather than doing real work on a multi-thousand-entry buffer.
func (g *GraphView) buildSeries(historyWindow int) []graphSeries {
	var out []graphSeries
	for _, t := range g.targets {
		v := t.GetViewWindow(historyWindow)
		out = append(out, icmpSeries{label: v.Host, history: v.History, lastRTT: v.LastRTT})
		if g.includePorts {
			for _, port := range v.PortResults {
				out = append(out, icmpSeries{label: fmt.Sprintf("%s %d/%s", v.Host, port.Port, port.Protocol), history: port.History, lastRTT: port.RTT})
			}
		}
	}
	return out
}

// windowMax returns the largest RTT value across all series within the current window.
func windowMax(series []graphSeries, windowPoints int) time.Duration {
	var max time.Duration
	for _, s := range series {
		hist, _ := s.seriesSnapshot()
		if len(hist) == 0 {
			continue
		}
		data := hist
		if len(data) > windowPoints {
			data = data[len(data)-windowPoints:]
		}
		for _, v := range data {
			if v > max {
				max = v
			}
		}
	}
	return max
}

// updateScale recalculates the auto-scale, applying hysteresis on shrink.
func (g *GraphView) updateScale(dataMax time.Duration, now time.Time) {
	newScale := computeYScale(dataMax, graphScaleFloorMs)
	if newScale.maxMs > g.currentScale.maxMs {
		// Expand immediately.
		g.currentScale = newScale
		g.scaleHoldUntil = now.Add(graphScaleHoldSecs * time.Second)
	} else if newScale.maxMs < g.currentScale.maxMs && now.After(g.scaleHoldUntil) {
		// Shrink only after hold period.
		g.currentScale = newScale
		g.scaleHoldUntil = now.Add(graphScaleHoldSecs * time.Second)
	}
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

func gridStepsForHeight(plotHeight int) (gy25, gy50, gy75, gy100 int) {
	totalSteps := plotHeight - 1
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
	return gy25, gy50, gy75, gy100
}

// preferredHeight is the pane height (borders included) at which every
// visible graph row gets graphPreferredRowHeight lines for the given inner
// width.
func (g *GraphView) preferredHeight(width int) int {
	_, _, visibleRows, _, _ := g.layout(width, graphMaxVisibleRows*graphPreferredRowHeight)
	return visibleRows*graphPreferredRowHeight + 2
}

func (g *GraphView) layout(width, height int) (numCols, numRowsTotal, visibleRows, colWidth, rowHeight int) {
	// Only the count is needed here (not the fetched history/label data),
	// and buildSeries maps 1:1 onto g.targets with no filtering, so read
	// the count directly rather than fetching a GetViewWindow snapshot per
	// target just to throw it away.
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
	g.Box.DrawForSubclass(screen, g)
	x, y, width, height := g.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Explicitly clear the inner rect to prevent rendering duplication
	for row := y; row < y+height; row++ {
		for col := x; col < x+width; col++ {
			screen.SetContent(col, row, ' ', nil, tcell.StyleDefault.Background(tcell.ColorBlack))
		}
	}

	// Compute time-based window width first so buildSeries can fetch
	// exactly this many trailing history points per target via
	// GetViewWindow, instead of the full history ring.
	timeBasedWidth := int(graphWindowSeconds * time.Second / g.interval)
	if timeBasedWidth < 1 {
		timeBasedWidth = 1
	}

	series := g.buildSeries(timeBasedWidth)
	if len(series) == 0 {
		return
	}

	// Update auto-scale from current window maximum across all series.
	g.updateScale(windowMax(series, timeBasedWidth), time.Now())
	yMaxDur := time.Duration(g.currentScale.maxMs * float64(time.Millisecond))

	numCols, numRowsTotal, visibleRows, colWidth, rowHeight := g.layout(width, height)
	if visibleRows == 0 {
		return
	}
	g.clampScroll(numRowsTotal, visibleRows)

	// Draw loop
	for r := 0; r < visibleRows; r++ {
		rowIndex := g.scrollRow + r
		baseY := y + (r * rowHeight)
		if baseY >= y+height {
			break
		}

		graphHeight := rowHeight - 2
		if graphHeight < 1 {
			graphHeight = 1
		}
		if baseY+1+graphHeight > y+height {
			graphHeight = (y + height) - (baseY + 1)
		}

		for c := 0; c < numCols; c++ {
			idx := rowIndex*numCols + c
			if idx >= len(series) {
				break
			}
			g.drawCell(screen, series[idx], graphCell{
				x: x + c*colWidth, y: baseY, width: colWidth, rowHeight: rowHeight,
				graphHeight: graphHeight, bottom: y + height,
			}, timeBasedWidth, yMaxDur)
		}
	}
}

// graphCell is one series' slot in the grid: a header line, the plot, and a
// separator on the slot's last line (when it fits above bottom).
type graphCell struct {
	x, y, width, rowHeight int
	graphHeight            int // plot rows available, already clipped to bottom
	bottom                 int // first screen row below the pane
}

func (g *GraphView) drawCell(screen tcell.Screen, s graphSeries, cell graphCell, windowPoints int, yMax time.Duration) {
	hist, lastRTT := s.seriesSnapshot()

	// Header: label + last RTT
	headerStr := fmt.Sprintf("% -20s %s", s.seriesLabel(), formatRTT(lastRTT))
	headerStr = truncateToDisplayWidth(headerStr, cell.width-2)
	tview.Print(screen, headerStr, cell.x, cell.y, cell.width-2, tview.AlignLeft, tcell.ColorYellow)

	graphX := cell.x
	graphY := cell.y + 1
	labelWidth := graphLabelWidth
	graphWidth := max(cell.width-labelWidth-2, graphMinWidth)

	data, hasData := projectDurationsToGraph(hist, windowPoints, graphWidth)
	plotY, plotHeight := adjustPlotArea(graphY, cell.graphHeight)

	gy25, gy50, gy75, gy100 := gridStepsForHeight(plotHeight)
	gridSteps := [4]int{gy25, gy50, gy75, gy100}
	gridYPos := make(map[int]bool)
	for i, val := range g.currentScale.grid {
		gy := gridSteps[i]
		py := plotY + (plotHeight - 1 - gy)
		if py >= plotY && py < plotY+plotHeight {
			gridYPos[gy] = true
			for gx := 0; gx < graphWidth; gx++ {
				screen.SetContent(graphX+gx, py, '·', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
			}
			tview.Print(screen, fmt.Sprintf("%dms", val), graphX+graphWidth+1, py, labelWidth, tview.AlignLeft, tcell.ColorGray)
		}
	}
	// 0ms label at bottom
	tview.Print(screen, "0ms", graphX+graphWidth+1, plotY+plotHeight-1, labelWidth, tview.AlignLeft, tcell.ColorGray)

	for i, val := range data {
		if hasData[i] {
			g.drawBar(screen, graphX+i, plotY, plotHeight, min(val, yMax), gridYPos)
		}
	}

	// Separator line between graph cells
	sepY := cell.y + cell.rowHeight - 1
	if sepY > graphY && sepY < cell.bottom {
		for sx := 0; sx < cell.width; sx++ {
			screen.SetContent(cell.x+sx, sepY, '─', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
		}
	}
}

// barRunes are the eighth-height blocks a bar's top cell is drawn with.
var barRunes = []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// drawBar draws one RTT sample (already capped at the scale maximum) as a
// column of block characters at screen column px, keeping grid dots visible
// above the bar.
func (g *GraphView) drawBar(screen tcell.Screen, px, plotY, plotHeight int, v time.Duration, gridYPos map[int]bool) {
	ratio := float64(v.Milliseconds()) / g.currentScale.maxMs
	if v > 0 && ratio < 0.05 {
		ratio = 0.05
	}
	totalLevels := int(ratio * float64(plotHeight*8))
	if v > 0 && totalLevels == 0 {
		totalLevels = 1
	}
	for gy := 0; gy < plotHeight; gy++ {
		py := plotY + (plotHeight - 1 - gy)
		level := totalLevels - (gy * 8)
		switch {
		case level >= 8:
			screen.SetContent(px, py, '█', nil, tcell.StyleDefault.Foreground(g.vividCyan))
		case level > 0:
			screen.SetContent(px, py, barRunes[level], nil, tcell.StyleDefault.Foreground(g.vividCyan))
		case gridYPos[gy]:
			screen.SetContent(px, py, '·', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
		}
	}
}
