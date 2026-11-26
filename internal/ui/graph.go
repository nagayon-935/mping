package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

const (
	graphMaxVisibleRows = 3
	graphLabelWidth     = fixedMsWidth
	graphMinWidth       = 10
	graphWindowSeconds  = 30
	graphScaleFloorMs   = 100.0 // minimum Y-axis upper bound in ms
	graphScaleHoldSecs  = 5     // seconds before scale is allowed to shrink
)

// graphSeries is the snapshot of one ping or TCP/UDP check plotted in a graph.
type graphSeries struct {
	label      string
	history    []time.Duration
	lastRTT    time.Duration
	thresholds stats.Thresholds
}

// GraphView is a custom primitive for rendering RTT graphs
type GraphView struct {
	*tview.Box
	targets      []*stats.TargetStats
	interval     time.Duration
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

// NewGraphView creates an ICMP RTT graph with a trailing 30-second window
// and an automatically scaled Y axis. interval is the positive sample interval.
func NewGraphView(targets []*stats.TargetStats, interval time.Duration) *GraphView {
	return &GraphView{
		Box:          tview.NewBox(),
		targets:      targets,
		interval:     interval,
		currentScale: computeYScale(0, graphScaleFloorMs),
	}
}

// buildDetailSeries snapshots the ping and port-check histories for the
// details graph, whose layout depends on the number of live ports.
func (g *GraphView) buildDetailSeries(historyWindow int) []graphSeries {
	out := make([]graphSeries, 0, len(g.targets))
	for _, t := range g.targets {
		v := t.GetViewWindow(historyWindow)
		out = append(out, graphSeries{label: v.Host, history: v.History, lastRTT: v.LastRTT, thresholds: v.Thresholds})
		for _, port := range v.PortResults {
			out = append(out, graphSeries{label: fmt.Sprintf("%s %d/%s", v.Host, port.Port, port.Protocol), history: port.History, lastRTT: port.RTT, thresholds: v.Thresholds})
		}
	}
	return out
}

// visiblePingSeries copies the visible histories and measures only the
// maximum for off-screen targets so the Y scale stays shared while scrolling.
func (g *GraphView) visiblePingSeries(start, end, historyWindow int) ([]graphSeries, time.Duration) {
	series := make([]graphSeries, 0, end-start)
	var peak time.Duration
	for i, t := range g.targets {
		if i < start || i >= end {
			peak = max(peak, t.PingWindowMax(historyWindow))
			continue
		}
		view := t.GetPingViewWindow(historyWindow)
		series = append(series, graphSeries{label: view.Host, history: view.History, lastRTT: view.LastRTT, thresholds: view.Thresholds})
	}
	return series, max(peak, windowMax(series, historyWindow))
}

// windowMax returns the largest RTT value across all series within the current window.
func windowMax(series []graphSeries, windowPoints int) time.Duration {
	var maxRTT time.Duration
	for _, s := range series {
		data := s.history
		if len(data) > windowPoints {
			data = data[len(data)-windowPoints:]
		}
		for _, v := range data {
			maxRTT = max(maxRTT, v)
		}
	}
	return maxRTT
}

// updateScale recalculates the auto-scale, applying hysteresis on shrink.
func (g *GraphView) updateScale(dataMax time.Duration, now time.Time) {
	newScale := computeYScale(dataMax, graphScaleFloorMs)
	// Expand immediately; shrink only after the hold period.
	if newScale.maxMs > g.currentScale.maxMs || (newScale.maxMs < g.currentScale.maxMs && now.After(g.scaleHoldUntil)) {
		g.currentScale = newScale
		g.scaleHoldUntil = now.Add(graphScaleHoldSecs * time.Second)
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

		// Without series or a drawable area layoutGraph has no visible rows,
		// so the scroll resets to the top.
		_, _, width, height := g.GetInnerRect()
		_, numRowsTotal, visibleRows, _, _ := layoutGraph(g.seriesCount(), width, height)
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

	// The box already paints the graph background in the normal black
	// theme. Keep the plot black if a caller customizes the surrounding box.
	if g.GetBackgroundColor() != tcell.ColorBlack {
		background := tcell.StyleDefault.Background(tcell.ColorBlack)
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				screen.SetContent(col, row, ' ', nil, background)
			}
		}
	}

	// Use the same trailing sample window for scaling and plotting.
	timeBasedWidth := 1
	if g.interval > 0 {
		timeBasedWidth = max(int(graphWindowSeconds*time.Second/g.interval), 1)
	}

	seriesCount := len(g.targets)
	var series []graphSeries
	if g.includePorts {
		series = g.buildDetailSeries(timeBasedWidth)
		seriesCount = len(series)
	}
	numCols, numRowsTotal, visibleRows, colWidth, rowHeight := layoutGraph(seriesCount, width, height)
	if visibleRows == 0 {
		return
	}
	g.clampScroll(numRowsTotal, visibleRows)

	start := g.scrollRow * numCols
	end := min(start+visibleRows*numCols, seriesCount)
	var peak time.Duration
	if g.includePorts {
		peak = windowMax(series, timeBasedWidth)
		series = series[start:end]
	} else {
		series, peak = g.visiblePingSeries(start, end, timeBasedWidth)
	}
	g.updateScale(peak, time.Now())
	yMaxDur := time.Duration(g.currentScale.maxMs * float64(time.Millisecond))

	// Draw loop
	for r := range visibleRows {
		baseY := y + (r * rowHeight)
		if baseY >= y+height {
			break
		}

		graphHeight := max(rowHeight-2, 1)
		if baseY+1+graphHeight > y+height {
			graphHeight = (y + height) - (baseY + 1)
		}

		for c := range numCols {
			idx := r*numCols + c
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

// gridRowSet stores up to a handful of Y gridline positions on the stack so
// drawCell/drawBar never allocate a map per chart per frame.
type gridRowSet struct {
	rows [8]int
	n    int
}

func (s *gridRowSet) add(gy int) {
	if s.n < len(s.rows) {
		s.rows[s.n] = gy
		s.n++
	}
}

func (s gridRowSet) has(gy int) bool {
	for i := range s.n {
		if s.rows[i] == gy {
			return true
		}
	}
	return false
}

// drawCell draws one series header, scaled plot, grid labels and separator.
func (g *GraphView) drawCell(screen tcell.Screen, s graphSeries, cell graphCell, windowPoints int, yMax time.Duration) {
	headerWidth := max(cell.width-2, 0)
	label := truncateToDisplayWidth(s.label, headerWidth)
	rtt := formatFixedMs(s.lastRTT)
	labelWidth := min(max(runewidth.StringWidth(label), 20), headerWidth-runewidth.StringWidth(rtt)-1)
	header := label
	if labelWidth >= runewidth.StringWidth(label) {
		header = formatCellText(label, labelWidth, tview.AlignLeft) + " " + rtt
	}
	tview.Print(screen, tview.Escape(header), cell.x, cell.y, headerWidth, tview.AlignLeft, tcell.ColorYellow)
	if loss, color := windowLoss(s.history, windowPoints, s.thresholds); loss != "" {
		offset := runewidth.StringWidth(header) + 2
		tview.Print(screen, loss, cell.x+offset, cell.y, max(headerWidth-offset, 0), tview.AlignLeft, color)
	}

	graphX := cell.x
	graphY := cell.y + 1
	axisWidth := min(graphLabelWidth, max(cell.width-graphMinWidth-2, 0))
	graphWidth := max(cell.width-axisWidth-2, 0)

	columns := projectDurationsToGraph(s.history, windowPoints, graphWidth)
	plotY, plotHeight := adjustPlotArea(graphY, cell.graphHeight)

	gridSteps := gridStepsForHeight(plotHeight)
	var gridYPos gridRowSet
	for i, val := range g.currentScale.grid {
		gy := gridSteps[i]
		py := plotY + (plotHeight - 1 - gy)
		if py >= plotY && py < plotY+plotHeight {
			gridYPos.add(gy)
			for gx := range graphWidth {
				screen.SetContent(graphX+gx, py, '·', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
			}
			tview.Print(screen, formatFixedMs(time.Duration(val)*time.Millisecond), graphX+graphWidth+1, py, axisWidth, tview.AlignLeft, tcell.ColorGray)
		}
	}
	// The axis origin is zero, independent of whether a sample is missing.
	tview.Print(screen, "0.00ms", graphX+graphWidth+1, plotY+plotHeight-1, axisWidth, tview.AlignLeft, tcell.ColorGray)

	for i, col := range columns {
		switch {
		case col.lost:
			// Every probe here was lost: a red × on the zero line, the same
			// mark as the Ping Monitor's sparkline.
			screen.SetContent(graphX+i, plotY+plotHeight-1, '×', nil, tcell.StyleDefault.Foreground(tcell.ColorRed))
		case col.has:
			color := vividCyan
			if col.someLost {
				color = lossPartialColor
			}
			g.drawBar(screen, graphX+i, plotY, plotHeight, min(col.value, yMax), gridYPos, color)
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

// windowLoss reports the lost probes among the trailing windowPoints
// samples ("loss 13%"), or "" when none were lost.
func windowLoss(history []time.Duration, windowPoints int, thresholds stats.Thresholds) (string, tcell.Color) {
	if len(history) > windowPoints {
		history = history[len(history)-windowPoints:]
	}
	lost := 0
	for _, v := range history {
		if v == 0 {
			lost++
		}
	}
	if lost == 0 {
		return "", tcell.ColorDefault
	}
	pct := float64(lost) * 100 / float64(len(history))
	color := tcell.ColorDefault
	if level := thresholds.Loss(pct); level != stats.LevelOK {
		color = levelColor(level)
	}
	return fmt.Sprintf("loss %.0f%%", pct), color
}

// barRunes are the eighth-height blocks a bar's top cell is drawn with.
var barRunes = []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// drawBar draws one RTT sample (already capped at the scale maximum) as a
// column of block characters at screen column px, keeping grid dots visible
// above the bar.
func (g *GraphView) drawBar(screen tcell.Screen, px, plotY, plotHeight int, v time.Duration, gridYPos gridRowSet, color tcell.Color) {
	ratio := float64(v) / float64(time.Millisecond) / g.currentScale.maxMs
	if v > 0 && ratio < 0.05 {
		ratio = 0.05
	}
	totalLevels := int(ratio * float64(plotHeight*8))
	if v > 0 && totalLevels == 0 {
		totalLevels = 1
	}
	for gy := range plotHeight {
		py := plotY + (plotHeight - 1 - gy)
		level := totalLevels - (gy * 8)
		switch {
		case level >= 8:
			screen.SetContent(px, py, '█', nil, tcell.StyleDefault.Foreground(color))
		case level > 0:
			screen.SetContent(px, py, barRunes[level], nil, tcell.StyleDefault.Foreground(color))
		case gridYPos.has(gy):
			screen.SetContent(px, py, '·', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
		}
	}
}
