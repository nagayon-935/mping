package ui

import (
	"time"
)

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
		x := max(int(float64(windowIdx)*float64(graphWidth-1)/float64(windowPoints-1)+0.5), 0)
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
	maxScroll := max(numRowsTotal-visibleRows, 0)
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

func gridStepsForHeight(plotHeight int) (gy25, gy50, gy75, gy100 int) {
	totalSteps := max(plotHeight-1, 1)
	baseStep := totalSteps / 4
	rem := totalSteps % 4
	seg := [4]int{baseStep, baseStep, baseStep, baseStep}
	for i := range rem {
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

	visibleRows = min(numRowsTotal, graphMaxVisibleRows)
	if visibleRows < 1 {
		visibleRows = 1
	}

	colWidth = width / numCols
	rowHeight = max(height/visibleRows, 2)
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
		rowHeight = max(height/visibleRows, 2)
	}

	return numCols, numRowsTotal, visibleRows, colWidth, rowHeight
}
