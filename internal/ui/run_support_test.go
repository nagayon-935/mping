package ui

import (
	"testing"
	"time"
)

// TestShouldRedraw covers the P7 dirty-redraw-gate decision matrix: redraw
// when the generation counter advanced, OR when redrawFloor has elapsed
// regardless of the counter, and skip only when neither is true.
func TestShouldRedraw(t *testing.T) {
	tests := []struct {
		name            string
		gen, lastGen    uint64
		sinceLastRedraw time.Duration
		want            bool
	}{
		{"unchanged and within floor: skip", 5, 5, redrawFloor / 2, false},
		{"changed and within floor: redraw", 6, 5, redrawFloor / 2, true},
		{"unchanged but floor elapsed: redraw", 5, 5, redrawFloor, true},
		{"unchanged but floor exceeded: redraw", 5, 5, redrawFloor * 2, true},
		{"changed and floor elapsed: redraw", 6, 5, redrawFloor, true},
		{"unchanged, just under floor: skip", 5, 5, redrawFloor - time.Millisecond, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRedraw(tt.gen, tt.lastGen, tt.sinceLastRedraw); got != tt.want {
				t.Errorf("shouldRedraw(gen=%d, lastGen=%d, since=%v) = %v, want %v",
					tt.gen, tt.lastGen, tt.sinceLastRedraw, got, tt.want)
			}
		})
	}
}
