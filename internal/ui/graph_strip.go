package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
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
// trailing windowPoints of history, or 0 when there are none.
func percentile95(history []time.Duration, windowPoints int) time.Duration {
	if len(history) > windowPoints {
		history = history[len(history)-windowPoints:]
	}
	var answered []time.Duration
	for _, v := range history {
		if v > 0 {
			answered = append(answered, v)
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
// top of scaleMs. The color says how much of the bucket was lost: red with
// a × when every probe was lost (100%), orange when some were, cyan when
// none were. The rune is blank without samples, ▲ above the scale, and
// otherwise a block whose height follows the slowest reply.
func stripCell(b stripBucket, scaleMs float64) (rune, tcell.Color) {
	if !b.has {
		return ' ', tcell.ColorDefault
	}
	if b.max == 0 {
		return '×', vividRed
	}
	color := vividCyan
	if b.lost {
		color = lossPartialColor
	}
	if float64(b.max)/float64(time.Millisecond) > scaleMs {
		return '▲', color
	}
	ratio := float64(b.max) / float64(time.Millisecond) / scaleMs
	return stripRunes[min(int(ratio*float64(len(stripRunes))), len(stripRunes)-1)], color
}

// lossPartialColor marks a chart column or sparkline cell where some, not
// all, probes were lost: the Warning color (see levelColor).
var lossPartialColor = levelColor(stats.LevelWarn)

// prepareSparkBuckets computes the p95 scale and bucket samples for a sparkline.
func prepareSparkBuckets(history []time.Duration, windowPoints, width int) ([]stripBucket, float64) {
	scaleMs := computeYScale(percentile95(history, windowPoints), stripScaleFloorMs).maxMs
	return bucketHistory(history, windowPoints, width), scaleMs
}

// sparkline draws the trailing windowPoints samples of history in width
// cells against the samples' own p95 scale, with the same marks as a strip
// (▲ above the scale, × lost). It is always width cells wide, blank where
// there are no samples yet, so the column keeps its width from the start.
func sparkline(history []time.Duration, windowPoints, width int) string {
	buckets, scaleMs := prepareSparkBuckets(history, windowPoints, width)
	runes := make([]rune, 0, width)
	for _, b := range buckets {
		r, _ := stripCell(b, scaleMs)
		runes = append(runes, r)
	}
	return string(runes)
}

// colorTag is the tview tag selecting exactly color, so a sparkline cell
// uses the same red as Critical values (vividRed, an RGB color) rather than
// the terminal's own palette "red".
func colorTag(color tcell.Color) string {
	return fmt.Sprintf("[#%06x]", color.Hex())
}

// sparklineStyled is sparkline with tview color tags around the cells with
// loss (orange for some, red for all), for the Ping Monitor cell whose own
// text color (cyan) draws the rest. It has the same width on screen.
func sparklineStyled(history []time.Duration, windowPoints, width int) string {
	buckets, scaleMs := prepareSparkBuckets(history, windowPoints, width)
	var b strings.Builder
	for _, bucket := range buckets {
		r, color := stripCell(bucket, scaleMs)
		switch color {
		case vividRed, lossPartialColor:
			fmt.Fprintf(&b, "%s%c[-]", colorTag(color), r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
