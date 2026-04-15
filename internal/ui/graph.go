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
	graphLabelWidth     = 6 // e.g. "100ms"
	graphMinWidth       = 10
	graphWindowSeconds  = 30
	graphYMax           = 100 * time.Millisecond
)

var graphGridValues = []int{25, 50, 75, 100}

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
			headerStr = truncateToDisplayWidth(headerStr, colWidth-2)

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

