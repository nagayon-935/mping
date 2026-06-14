package ui

import (
	"math"
	"time"
)

// yScale describes the Y-axis range and grid labels for the RTT graph.
type yScale struct {
	maxMs float64 // Y-axis upper bound in ms
	grid  [4]int  // grid label values (ms) at 1/4, 2/4, 3/4, 4/4 of the range
}

// niceCeil rounds x up to the nearest "nice" number (1, 2, 2.5, 5, 10, ...).
func niceCeil(x float64) float64 {
	if x <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(x))
	base := math.Pow(10, exp)
	f := x / base
	var nf float64
	switch {
	case f <= 1:
		nf = 1
	case f <= 2:
		nf = 2
	case f <= 2.5:
		nf = 2.5
	case f <= 5:
		nf = 5
	default:
		nf = 10
	}
	return nf * base
}

// computeYScale calculates a stable Y-axis scale from the observed maximum RTT.
// floorMs sets the minimum upper bound (use 100 to preserve legacy behaviour).
func computeYScale(dataMax time.Duration, floorMs float64) yScale {
	// Apply 10% headroom to the raw data before clamping to the floor.
	// This keeps values near the ceiling from touching the top of the graph
	// while still yielding exact nice numbers for values like 450ms→500ms.
	target := dataMax.Seconds() * 1000 * 1.10
	if target < floorMs {
		target = floorMs
	}
	upper := niceCeil(target)

	g0 := int(math.Round(upper / 4))
	g1 := int(math.Round(upper / 2))
	g2 := int(math.Round(upper * 3 / 4))
	g3 := int(upper)

	// Ensure strictly monotonic grid values (can happen with 2.5-family niceceils).
	if g0 < 1 {
		g0 = 1
	}
	if g1 <= g0 {
		g1 = g0 + 1
	}
	if g2 <= g1 {
		g2 = g1 + 1
	}
	if g3 <= g2 {
		g3 = g2 + 1
	}

	return yScale{
		maxMs: upper,
		grid:  [4]int{g0, g1, g2, g3},
	}
}
