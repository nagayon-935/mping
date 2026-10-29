package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

const (
	graphMaxVisibleRows = 3
	graphLabelWidth     = 7 // e.g. "1000ms"
	graphMinWidth       = 10
	graphWindowSeconds  = 30
	graphScaleFloorMs   = 100.0 // minimum Y-axis upper bound in ms
	graphScaleHoldSecs  = 5     // seconds before scale is allowed to shrink
)

// graphSeries is a single RTT data series for the graph: an ICMP target or
// one of its TCP/UDP port checks. It holds a pre-fetched snapshot rather than
// a live *stats.TargetStats reference: buildSeries takes exactly one
// GetViewWindow() call per target, and the draw code just reads the
// already-fetched fields instead of each re-fetching from TargetStats
// (previously 3 separate GetView() calls per target per Draw — one for the
// label, one directly, one via windowMax's own snapshot call).
type graphSeries struct {
	label   string
	history []time.Duration
	lastRTT time.Duration
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

func NewGraphView(targets []*stats.TargetStats, interval time.Duration) *GraphView {
	return &GraphView{
		Box:          tview.NewBox(),
		targets:      targets,
		interval:     interval,
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
		out = append(out, graphSeries{label: v.Host, history: v.History, lastRTT: v.LastRTT})
		if g.includePorts {
			for _, port := range v.PortResults {
				out = append(out, graphSeries{label: fmt.Sprintf("%s %d/%s", v.Host, port.Port, port.Protocol), history: port.History, lastRTT: port.RTT})
			}
		}
	}
	return out
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
	timeBasedWidth := max(int(graphWindowSeconds*time.Second/g.interval), 1)

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
	for r := range visibleRows {
		rowIndex := g.scrollRow + r
		baseY := y + (r * rowHeight)
		if baseY >= y+height {
			break
		}

		graphHeight := max(rowHeight-2, 1)
		if baseY+1+graphHeight > y+height {
			graphHeight = (y + height) - (baseY + 1)
		}

		for c := range numCols {
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
	// Header: label + last RTT
	headerStr := fmt.Sprintf("% -20s %s", s.label, formatRTT(s.lastRTT))
	headerStr = truncateToDisplayWidth(headerStr, cell.width-2)
	tview.Print(screen, headerStr, cell.x, cell.y, cell.width-2, tview.AlignLeft, tcell.ColorYellow)

	graphX := cell.x
	graphY := cell.y + 1
	labelWidth := graphLabelWidth
	graphWidth := max(cell.width-labelWidth-2, graphMinWidth)

	data, hasData := projectDurationsToGraph(s.history, windowPoints, graphWidth)
	plotY, plotHeight := adjustPlotArea(graphY, cell.graphHeight)

	gy25, gy50, gy75, gy100 := gridStepsForHeight(plotHeight)
	gridSteps := [4]int{gy25, gy50, gy75, gy100}
	gridYPos := make(map[int]bool)
	for i, val := range g.currentScale.grid {
		gy := gridSteps[i]
		py := plotY + (plotHeight - 1 - gy)
		if py >= plotY && py < plotY+plotHeight {
			gridYPos[gy] = true
			for gx := range graphWidth {
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
	for gy := range plotHeight {
		py := plotY + (plotHeight - 1 - gy)
		level := totalLevels - (gy * 8)
		switch {
		case level >= 8:
			screen.SetContent(px, py, '█', nil, tcell.StyleDefault.Foreground(vividCyan))
		case level > 0:
			screen.SetContent(px, py, barRunes[level], nil, tcell.StyleDefault.Foreground(vividCyan))
		case gridYPos[gy]:
			screen.SetContent(px, py, '·', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
		}
	}
}
