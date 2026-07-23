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

type icmpSeries struct{ t *stats.TargetStats }

func (s icmpSeries) seriesLabel() string {
	return s.t.GetView().Host
}
func (s icmpSeries) seriesSnapshot() ([]time.Duration, time.Duration) {
	v := s.t.GetView()
	return v.History, v.LastRTT
}

// GraphView is a custom primitive for rendering RTT graphs
type GraphView struct {
	*tview.Box
	targets   []*stats.TargetStats
	interval  time.Duration
	vividCyan tcell.Color
	vividRed  tcell.Color
	scrollRow int

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

// buildSeries constructs the list of graphSeries from current target state.
// Called at the start of each Draw so that dynamically added targets appear.
func (g *GraphView) buildSeries() []graphSeries {
	var out []graphSeries
	for _, t := range g.targets {
		out = append(out, icmpSeries{t: t})
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

func (g *GraphView) layout(width, height int) (numCols, numRowsTotal, visibleRows, colWidth, rowHeight int) {
	numTargets := len(g.buildSeries())
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

	series := g.buildSeries()
	if len(series) == 0 {
		return
	}

	// Compute time-based window width once (shared across all cells).
	timeBasedWidth := int(graphWindowSeconds * time.Second / g.interval)
	if timeBasedWidth < 1 {
		timeBasedWidth = 1
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

			baseX := x + (c * colWidth)
			s := series[idx]
			hist, lastRTT := s.seriesSnapshot()

			// Header: label + last RTT
			headerStr := fmt.Sprintf("% -20s %s", s.seriesLabel(), formatRTT(lastRTT))
			headerStr = truncateToDisplayWidth(headerStr, colWidth-2)
			tview.Print(screen, headerStr, baseX, baseY, colWidth-2, tview.AlignLeft, tcell.ColorYellow)

			graphX := baseX
			graphY := baseY + 1
			labelWidth := graphLabelWidth
			graphWidth := colWidth - labelWidth - 2
			if graphWidth < graphMinWidth {
				graphWidth = graphMinWidth
			}

			data, hasData := projectDurationsToGraph(hist, timeBasedWidth, graphWidth)

			plotY, plotHeight := adjustPlotArea(graphY, graphHeight)
			rangeVal := g.currentScale.maxMs // ms

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

			if len(data) > 0 {
				chars := []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
				for i, val := range data {
					if !hasData[i] {
						continue
					}
					px := graphX + i
					v := val
					if v > yMaxDur {
						v = yMaxDur
					}
					ratio := float64(v.Milliseconds()) / rangeVal
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
						var ch rune
						if level <= 0 {
							if gridYPos[gy] {
								ch = '·'
							} else {
								ch = ' '
							}
						} else if level >= 8 {
							ch = '█'
						} else {
							ch = chars[level]
						}
						if ch != ' ' {
							color := g.vividCyan
							if ch == '·' {
								color = tcell.ColorGray
							}
							screen.SetContent(px, py, ch, nil, tcell.StyleDefault.Foreground(color))
						}
					}
				}
			}

			// Separator line between graph cells
			sepY := baseY + rowHeight - 1
			if sepY > graphY && sepY < y+height {
				for sx := 0; sx < colWidth; sx++ {
					screen.SetContent(baseX+sx, sepY, '─', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
				}
			}
		}
	}
}
