package ui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nagayon-935/mping/internal/stats"
)

// TestRunRoutePaneFollowsLiveMode checks that a route mode switched after a
// reload (TargetSet.Route) renames and re-renders the route pane, folded or
// not.
func TestRunRoutePaneFollowsLiveMode(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.MTR().EnsureLen(2)
	target.MTR().RecordReply(1, "10.0.0.1", "", "", "", 5*time.Millisecond)
	target.MTR().RecordReply(2, "1.1.1.1", "", "", "", 9*time.Millisecond)
	var mode atomic.Int32
	mode.Store(int32(RoutePath))
	h := startTUI(t, RunOptions{
		Targets: []*stats.TargetStats{target},
		Route:   RoutePath,
		TargetSource: func() TargetSet {
			return TargetSet{Targets: []*stats.TargetStats{target}, Route: RouteMode(mode.Load())}
		},
	}, 200, 50)
	h.waitFrame("the path-only route", requireAll("Traceroute Monitor", "10.0.0.1 -> 1.1.1.1"))

	mode.Store(int32(RouteMonitor))
	frame := h.waitFrame("the per-hop route", requireAll("MTR Monitor", "Loss%", "1.1.1.1"))
	if strings.Contains(frame, "Traceroute Monitor") {
		t.Fatalf("the old pane title remains:\n%s", frame)
	}

	h.key(tcell.KeyTab) // Ping Monitor -> MTR Monitor
	h.press('f')
	h.wait("[+]  MTR Monitor ")
	mode.Store(int32(RoutePath))
	h.wait("[+]  Traceroute Monitor ")
	h.quit()
}

// TestPullTargetsRouteMode checks that RouteOff in the target set keeps the
// current mode, and that a run without a route pane never gains one.
func TestPullTargetsRouteMode(t *testing.T) {
	set := TargetSet{Route: RouteOff}
	withPane := newTUI(RunOptions{Route: RouteMonitor, TargetSource: func() TargetSet { return set }})
	withPane.pullTargets()
	if withPane.opts.Route != RouteMonitor {
		t.Fatalf("RouteOff changed the mode to %v", withPane.opts.Route)
	}
	set.Route = RoutePath
	withPane.pullTargets()
	if withPane.opts.Route != RoutePath {
		t.Fatalf("mode = %v, want RoutePath", withPane.opts.Route)
	}

	withoutPane := newTUI(RunOptions{TargetSource: func() TargetSet { return TargetSet{Route: RouteMonitor} }})
	withoutPane.pullTargets()
	if withoutPane.opts.Route != RouteOff {
		t.Fatalf("a run without a route pane switched to %v", withoutPane.opts.Route)
	}
}
