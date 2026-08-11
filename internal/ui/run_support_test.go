package ui

import (
	"testing"
	"time"
)

// TestRefreshTickInterval pins the refresh loop's tick period. The gate in
// shouldRedraw can only *suppress* a redraw, never bring one forward, so the
// tick period is the hard upper bound on how stale the table can get. A bare
// interval/2 breaks redrawFloor's promise (a 10s ping interval would tick
// every 5s, leaving the graph's auto-scale shrink unredrawn for 5x its 1s
// floor); capping the tick at redrawFloor restores it.
func TestRefreshTickInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"below minUIRefresh uses the fast rate", 50 * time.Millisecond, fastUIRefresh},
		{"at minUIRefresh halves the interval", minUIRefresh, minUIRefresh / 2},
		{"short interval halves", time.Second, 500 * time.Millisecond},
		{"half-interval exactly at the floor is kept", 2 * redrawFloor, redrawFloor},
		{"long interval is capped at the floor", 10 * time.Second, redrawFloor},
		{"very long interval is still capped", time.Minute, redrawFloor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refreshTickInterval(tt.interval); got != tt.want {
				t.Errorf("refreshTickInterval(%v) = %v, want %v", tt.interval, got, tt.want)
			}
		})
	}
}

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
