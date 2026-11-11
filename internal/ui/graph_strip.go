package ui

import (
	"fmt"
	"slices"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

// The main RTT pane draws one line per series ("strips") so every target is
// visible at once; the host details pane keeps the tall grid chart.
const (
	stripWindowSeconds = 300  // trailing window of a strip
	stripLabelMaxWidth = 20   // widest series label
	stripStatsWidth    = 22   // "p95 48.9ms  max ▲680ms"
	stripScaleFloorMs  = 10.0 // smallest strip scale top, in ms
)

// stripRunes are the eighth-height blocks a strip sample is drawn with.
var stripRunes = []rune("▁▂▃▄▅▆▇█")

// stripBucket is one screen column of a strip: the slowest reply among the
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

// stripLabelWidth is the label column width: the widest label, capped.
func stripLabelWidth(series []graphSeries) int {
	w := 0
	for _, s := range series {
		w = max(w, runewidth.StringWidth(s.label))
	}
	return min(w, stripLabelMaxWidth)
}

// drawStrips draws a legend line and then one strip per series, from the
// scrolled-to series down, in the rectangle at (x, y).
func (g *GraphView) drawStrips(screen tcell.Screen, series []graphSeries, x, y, width, height, windowPoints int) {
	scaleMs := g.currentScale.maxMs
	legend := fmt.Sprintf("RTT %s · scale 0–%.0fms (p95) · ▲ above scale · × lost", formatWindow(windowPoints, g.interval), scaleMs)
	tview.Print(screen, legend, x, y, width, tview.AlignLeft, tcell.ColorGray)

	labelW := stripLabelWidth(series)
	barsW := width - labelW - stripStatsWidth - 2
	if barsW < graphMinWidth {
		barsW = max(width-labelW-1, 1)
	}
	rows := max(height-1, 0)
	g.clampScroll(len(series), rows)
	for r := range rows {
		idx := g.scrollRow + r
		if idx >= len(series) {
			break
		}
		s := series[idx]
		rowY := y + 1 + r
		tview.Print(screen, truncateToDisplayWidth(s.label, labelW), x, rowY, labelW, tview.AlignLeft, tcell.ColorYellow)
		for i, b := range bucketHistory(s.history, windowPoints, barsW) {
			ch, color := stripCell(b, scaleMs)
			screen.SetContent(x+labelW+1+i, rowY, ch, nil, tcell.StyleDefault.Foreground(color).Background(tcell.ColorBlack))
		}
		if statsX := x + labelW + 1 + barsW + 1; statsX < x+width {
			tview.Print(screen, stripStats(s.history, windowPoints, scaleMs), statsX, rowY, x+width-statsX, tview.AlignLeft, tcell.ColorGray)
		}
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
