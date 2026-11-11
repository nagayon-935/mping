package ui

import (
	"fmt"
	"slices"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

// The main RTT pane draws a small chart per series ("mini" mode): a header
// line and miniPlotRows of bars over the last stripWindowSeconds. It is shown
// only while every series gets its cell; otherwise the pane folds and the
// Ping Monitor's sparkline column takes its place (see paneControls.fit).
// The host details pane keeps the tall grid chart.
const (
	stripWindowSeconds = 300  // trailing window of a mini chart
	stripScaleFloorMs  = 10.0 // smallest mini chart / sparkline scale top, in ms
	miniPlotRows       = 3    // bar rows of a mini chart
	miniCellRows       = miniPlotRows + 1
	miniLabelWidth     = 6  // "680ms" scale label at the right
	miniMinCellWidth   = 40 // narrowest cell for two cells side by side
)

// stripRunes are the eighth-height blocks a strip sample is drawn with.
var stripRunes = []rune("▁▂▃▄▅▆▇█")

// stripBucket is one screen column of a chart: the slowest reply among the
// samples it covers, and whether any of them was lost.
type stripBucket struct {
	max  time.Duration
	lost bool
	has  bool
}

// bucketHistory spreads the trailing windowPoints samples of history over
// width columns, right-aligned so the newest sample is in the last column.
// A zero sample is a lost probe.
func bucketHistory(history []time.Duration, windowPoints, width int) []stripBucket {
	if width <= 0 || windowPoints <= 0 {
		return nil
	}
	buckets := make([]stripBucket, width)
	if len(history) > windowPoints {
		history = history[len(history)-windowPoints:]
	}
	offset := windowPoints - len(history)
	for i, v := range history {
		b := &buckets[min((offset+i)*width/windowPoints, width-1)]
		b.has = true
		if v == 0 {
			b.lost = true
		} else {
			b.max = max(b.max, v)
		}
	}
	return buckets
}

// percentile95 returns the 95th percentile of the answered samples in the
// trailing windowPoints of each history, or 0 when there are none.
func percentile95(histories [][]time.Duration, windowPoints int) time.Duration {
	var answered []time.Duration
	for _, h := range histories {
		if len(h) > windowPoints {
			h = h[len(h)-windowPoints:]
		}
		for _, v := range h {
			if v > 0 {
				answered = append(answered, v)
			}
		}
	}
	if len(answered) == 0 {
		return 0
	}
	slices.Sort(answered)
	idx := (len(answered)*95+99)/100 - 1
	return answered[max(idx, 0)]
}

// stripCell is the rune and color one bucket is drawn with against a scale
// top of scaleMs: blank without samples, a red × when every sample was lost,
// a red ▲ above the scale, otherwise a block whose height follows the RTT
// (red when some samples in it were lost).
func stripCell(b stripBucket, scaleMs float64) (rune, tcell.Color) {
	switch {
	case !b.has:
		return ' ', tcell.ColorDefault
	case b.max == 0:
		return '×', vividRed
	case float64(b.max)/float64(time.Millisecond) > scaleMs:
		return '▲', vividRed
	}
	ratio := float64(b.max) / float64(time.Millisecond) / scaleMs
	r := stripRunes[min(int(ratio*float64(len(stripRunes))), len(stripRunes)-1)]
	if b.lost {
		return r, vividRed
	}
	return r, vividCyan
}

// stripStats summarizes one series' window for the right of its strip.
func stripStats(history []time.Duration, windowPoints int, scaleMs float64) string {
	if len(history) > windowPoints {
		history = history[len(history)-windowPoints:]
	}
	lost, maxRTT := 0, time.Duration(0)
	for _, v := range history {
		if v == 0 {
			lost++
		}
		maxRTT = max(maxRTT, v)
	}
	if len(history) > 0 && lost == len(history) {
		return "loss 100%"
	}
	if maxRTT == 0 {
		return ""
	}
	mark := " "
	if float64(maxRTT)/float64(time.Millisecond) > scaleMs {
		mark = "▲"
	}
	p95 := percentile95([][]time.Duration{history}, windowPoints)
	return fmt.Sprintf("p95 %s  max %s%s", formatShortMs(p95), mark, formatShortMs(maxRTT))
}

// formatShortMs formats d in whole milliseconds, or with one decimal below
// 10 ms.
func formatShortMs(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	if ms < 10 {
		return fmt.Sprintf("%.1fms", ms)
	}
	return fmt.Sprintf("%.0fms", ms)
}

// miniLayout returns how many mini-chart columns fit in width and how many
// rows of cells n series need.
func miniLayout(n, width int) (cols, rows int) {
	if n <= 0 {
		return 1, 0
	}
	cols = 1
	if n > 1 && width >= 2*miniMinCellWidth {
		cols = 2
	}
	return cols, (n + cols - 1) / cols
}

// miniCell is one screen cell of a mini chart column.
type miniCell struct {
	r     rune
	color tcell.Color
}

// miniColumn draws one bucket as miniPlotRows cells, bottom first: blank
// without samples, a red × at the bottom when every sample was lost, a full
// bar topped by a red ▲ above the scale, otherwise a bar in eighths (red
// when some samples in it were lost).
func miniColumn(b stripBucket, scaleMs float64) [miniPlotRows]miniCell {
	var col [miniPlotRows]miniCell
	for i := range col {
		col[i] = miniCell{' ', tcell.ColorDefault}
	}
	switch {
	case !b.has:
		return col
	case b.max == 0:
		col[0] = miniCell{'×', vividRed}
		return col
	case float64(b.max)/float64(time.Millisecond) > scaleMs:
		for i := range col {
			col[i] = miniCell{'█', vividCyan}
		}
		col[miniPlotRows-1] = miniCell{'▲', vividRed}
		return col
	}
	color := vividCyan
	if b.lost {
		color = vividRed
	}
	levels := max(int(float64(b.max)/float64(time.Millisecond)/scaleMs*miniPlotRows*8), 1)
	for i := range col {
		switch lvl := levels - i*8; {
		case lvl >= 8:
			col[i] = miniCell{'█', color}
		case lvl > 0:
			col[i] = miniCell{stripRunes[lvl-1], color}
		}
	}
	return col
}

// drawMiniGrid draws a legend line and then one small chart per series
// (a header line and miniPlotRows of bars over the window, each on its own
// p95 scale), two to a row when the width allows, from the scrolled-to row.
func (g *GraphView) drawMiniGrid(screen tcell.Screen, series []graphSeries, x, y, width, height, windowPoints int) {
	legend := fmt.Sprintf("RTT %s · p95 scale per host · ▲ above scale · × lost", formatWindow(windowPoints, g.interval))
	tview.Print(screen, legend, x, y, width, tview.AlignLeft, tcell.ColorGray)

	cols, rows := miniLayout(len(series), width)
	visible := max((height-1)/miniCellRows, 0)
	g.clampScroll(rows, visible)
	cellW := width / cols
	for r := range visible {
		for c := range cols {
			idx := (g.scrollRow+r)*cols + c
			if idx >= len(series) {
				return
			}
			g.drawMiniCell(screen, series[idx], x+c*cellW, y+1+r*miniCellRows, cellW, windowPoints)
		}
	}
}

// drawMiniCell draws one series: its label, latest RTT and window summary,
// then the bars with the scale top, middle and zero at the right.
func (g *GraphView) drawMiniCell(screen tcell.Screen, s graphSeries, x, y, width, windowPoints int) {
	scaleMs := computeYScale(percentile95([][]time.Duration{s.history}, windowPoints), stripScaleFloorMs).maxMs
	last := "-"
	if s.lastRTT > 0 {
		last = formatShortMs(s.lastRTT)
	}
	label := truncateToDisplayWidth(s.label, max(width/2, 8))
	tview.Print(screen, label, x+1, y, width-1, tview.AlignLeft, tcell.ColorYellow)
	info := fmt.Sprintf("  %s  %s", last, stripStats(s.history, windowPoints, scaleMs))
	infoX := x + 1 + runewidth.StringWidth(label)
	tview.Print(screen, info, infoX, y, max(x+width-infoX-1, 0), tview.AlignLeft, tcell.ColorGray)

	plotW := max(width-miniLabelWidth-3, 1)
	for i, b := range bucketHistory(s.history, windowPoints, plotW) {
		for row, cell := range miniColumn(b, scaleMs) {
			screen.SetContent(x+1+i, y+miniPlotRows-row, cell.r, nil, tcell.StyleDefault.Foreground(cell.color).Background(tcell.ColorBlack))
		}
	}
	labels := [miniPlotRows]string{formatShortMs(time.Duration(scaleMs * float64(time.Millisecond))), "", "0"}
	for row, text := range labels {
		tview.Print(screen, text, x+2+plotW, y+1+row, miniLabelWidth, tview.AlignLeft, tcell.ColorGray)
	}
}

// formatWindow names a trailing window of windowPoints samples taken every
// interval, e.g. "5m".
func formatWindow(windowPoints int, interval time.Duration) string {
	d := (time.Duration(windowPoints) * interval).Round(time.Second)
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}

// sparkline draws the trailing windowPoints samples of history in width
// cells against the samples' own p95 scale, with the same marks as a strip
// (▲ above the scale, × lost). It is always width cells wide, blank where
// there are no samples yet, so the column keeps its width from the start.
func sparkline(history []time.Duration, windowPoints, width int) string {
	scaleMs := computeYScale(percentile95([][]time.Duration{history}, windowPoints), stripScaleFloorMs).maxMs
	runes := make([]rune, 0, width)
	for _, b := range bucketHistory(history, windowPoints, width) {
		r, _ := stripCell(b, scaleMs)
		runes = append(runes, r)
	}
	return string(runes)
}
