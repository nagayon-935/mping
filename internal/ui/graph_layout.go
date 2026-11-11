package ui

import (
	"time"
)

// graphColumn is one screen column of an RTT chart: the slowest reply
// among the samples it shows (value, when has), and whether all of them
// (lost) or some of them (someLost) were lost probes.
type graphColumn struct {
	value    time.Duration
	has      bool
	lost     bool
	someLost bool
}

// projectDurationsToGraph maps the trailing sample window to graph columns,
// right-aligned so the newest sample is in the last column. A zero sample is
// a lost probe. Between two replies that land columns apart the RTT is
// interpolated; next to a lost probe the gap keeps the previous sample's
// state instead, so a loss never shows as a slope down to zero.
func projectDurationsToGraph(data []time.Duration, windowPoints, graphWidth int) []graphColumn {
	if windowPoints <= 0 || graphWidth <= 0 || len(data) == 0 {
		return nil
	}
	if len(data) > windowPoints {
		data = data[len(data)-windowPoints:]
	}
	cols := make([]graphColumn, graphWidth)
	replies := make([]int, graphWidth)
	losses := make([]int, graphWidth)
	put := func(x int, v time.Duration) {
		cols[x].has = true
		if v == 0 {
			losses[x]++
		} else {
			replies[x]++
			cols[x].value = max(cols[x].value, v)
		}
	}
	xOf := func(windowIdx int) int {
		if windowPoints == 1 {
			return graphWidth - 1
		}
		return min(max(int(float64(windowIdx)*float64(graphWidth-1)/float64(windowPoints-1)+0.5), 0), graphWidth-1)
	}
	offset := windowPoints - len(data)
	prevX, prevV := -1, time.Duration(0)
	for i, v := range data {
		x := xOf(offset + i)
		if prevX >= 0 && x > prevX+1 {
			for px := prevX + 1; px < x; px++ {
				if prevV > 0 && v > 0 {
					ratio := float64(px-prevX) / float64(x-prevX)
					put(px, prevV+time.Duration(float64(v-prevV)*ratio))
				} else {
					put(px, prevV)
				}
			}
		}
		put(x, v)
		prevX, prevV = x, v
	}
	for x := range cols {
		cols[x].lost = losses[x] > 0 && replies[x] == 0
		cols[x].someLost = losses[x] > 0 && replies[x] > 0
	}
	return cols
}

func (g *GraphView) clampScroll(numRowsTotal, visibleRows int) {
	maxScroll := max(numRowsTotal-visibleRows, 0)
	if g.scrollRow < 0 {
		g.scrollRow = 0
	} else if g.scrollRow > maxScroll {
		g.scrollRow = maxScroll
	}
}

// adjustPlotArea fits the plot within its cell with evenly spaced quarter grids.
func adjustPlotArea(graphY, graphHeight int) (plotY, plotHeight int) {
	plotHeight = graphHeight
	plotY = graphY
	if plotHeight > 1 {
		// Ensure equal spacing by making (plotHeight-1) divisible by 4.
		desiredSteps := max(((plotHeight-1)/4)*4, 1)
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

// gridStepsForHeight returns quarter-grid offsets from the bottom of the plot,
// distributing leftover rows among the lower segments first.
func gridStepsForHeight(plotHeight int) [4]int {
	totalSteps := max(plotHeight-1, 1)
	baseStep := totalSteps / 4
	rem := totalSteps % 4
	seg := [4]int{baseStep, baseStep, baseStep, baseStep}
	for i := range rem {
		seg[i]++
	}
	return [4]int{seg[0], seg[0] + seg[1], seg[0] + seg[1] + seg[2], totalSteps}
}

// preferredHeight is the pane height (borders included) at which every
// visible graph row gets graphPreferredRowHeight lines for the given inner
// width.
func (g *GraphView) preferredHeight(width int) int {
	_, _, visibleRows, _, _ := layoutGraph(g.seriesCount(), width, graphMaxVisibleRows*graphPreferredRowHeight)
	return visibleRows*graphPreferredRowHeight + 2
}

// seriesCount is len(g.buildSeries(...)) without fetching any history or
// label data: one series per target, plus one per port check when
// includePorts is set (Host Details).
func (g *GraphView) seriesCount() int {
	n := len(g.targets)
	if g.includePorts {
		for _, t := range g.targets {
			n += t.PortCount()
		}
	}
	return n
}

// layoutGraph arranges numSeries graph cells in a width×height area.
func layoutGraph(numSeries, width, height int) (numCols, numRowsTotal, visibleRows, colWidth, rowHeight int) {
	if numSeries == 0 || width <= 0 || height <= 0 {
		return 1, 0, 0, 0, 0
	}

	numCols = 1
	if numSeries > 1 {
		numCols = 2
	}
	minCellWidth := graphMinWidth + graphLabelWidth + 2
	if numCols == 2 && width < minCellWidth*2 {
		numCols = 1
	}

	numRowsTotal = (numSeries + numCols - 1) / numCols

	// numSeries >= 1 here, so numRowsTotal >= 1 and so is visibleRows.
	visibleRows = min(numRowsTotal, graphMaxVisibleRows)

	colWidth = width / numCols
	rowHeight = max(height/visibleRows, 2)
	for visibleRows > 1 {
		graphHeight := rowHeight - 2
		if graphHeight >= 5 {
			break
		}
		visibleRows-- // stays >= 1: the loop only runs while it is > 1
		rowHeight = max(height/visibleRows, 2)
	}

	return numCols, numRowsTotal, visibleRows, colWidth, rowHeight
}
