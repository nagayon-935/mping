package ui

import (
	"testing"
	"time"

	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
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
		{"non-positive interval falls back to the fast rate", 0, fastUIRefresh},
		{"below minUIRefresh uses the fast rate", 50 * time.Millisecond, fastUIRefresh},
		{"at minUIRefresh halves the interval", minUIRefresh, minUIRefresh / 2},
		// minUIRefresh/2 happens to equal fastUIRefresh, so the case above
		// passes under either branch. 300ms is the smallest round value where
		// halving and the fast rate disagree, which is what pins the "<".
		{"just above minUIRefresh takes the halving branch", 300 * time.Millisecond, 150 * time.Millisecond},
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

// TestStartRefreshLoop_RedrawsOnlyWhenStatsChange runs the real refresh loop
// against a session nobody drains and counts the table updates it posts: one
// for the first tick, none while nothing changes (well inside redrawFloor),
// and another once a statistic changes. shouldRedraw alone is covered by
// TestShouldRedraw; this pins that the loop honors it.
func TestStartRefreshLoop_RedrawsOnlyWhenStatsChange(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	// Bump the global generation so the first tick differs from the loop's
	// initial lastGen of 0 whatever ran before.
	target.IncSent()
	target.OnSuccess(10*time.Millisecond, 64)
	tr := newTestTableRenderer([]*stats.TargetStats{target}, 200)
	session := newUISession()
	t.Cleanup(func() { session.Stop(); session.Wait() })

	startRefreshLoop(tview.NewApplication(), tr, tview.NewTextView(), 0, nil, nil, nil, tr.vs, session)

	pollUntil(t, "the first redraw", func() bool { return len(session.updates) == 1 })
	first := time.Now()

	// Nothing changes for two and a half ticks (refreshTickInterval(0) is
	// fastUIRefresh): the skipped ticks must not post another update. The
	// elapsed-time guard only tolerates a scheduler stall long enough for
	// redrawFloor to legitimately force a redraw.
	time.Sleep(2*fastUIRefresh + fastUIRefresh/2)
	if n := len(session.updates); n != 1 && time.Since(first) < redrawFloor/2 {
		t.Fatalf("%d updates posted with no stats change, want 1", n)
	}

	target.IncSent()
	pollUntil(t, "a redraw after a stats change", func() bool { return len(session.updates) >= 2 })
}
