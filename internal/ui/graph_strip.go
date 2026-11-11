package ui

import (
	"slices"
	"time"

	"github.com/gdamore/tcell/v2"
)

// The Ping Monitor's RTT sparkline column draws the trailing samples of
// each target in one row of eighth-height blocks.

// stripScaleFloorMs is the smallest sparkline scale top, in ms.
const stripScaleFloorMs = 10.0

// stripRunes are the eighth-height blocks a strip sample is drawn with.
var stripRunes = []rune("▁▂▃▄▅▆▇█")

// stripBucket is one screen column of a sparkline: the slowest reply among the
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
