package ui

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
)

// TestRunKeyboardStop presses s and expects the stopped footer. A second s
// must be ignored: the R pressed after it runs on the same FIFO worker, so
// when its callback fires any stop wrongly queued by the second s has
// already been counted.
func TestRunKeyboardStop(t *testing.T) {
	tests := []struct {
		name   string
		onStop bool
	}{
		{"calls OnStop once", true},
		{"works without an OnStop callback", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stops atomic.Int32
			reset := make(chan struct{}, 1)
			opts := RunOptions{
				Targets: []*stats.TargetStats{stats.NewTargetStats("example.com")},
				OnReset: func() { reset <- struct{}{} },
			}
			if tt.onStop {
				opts.OnStop = func() { stops.Add(1) }
			}
			h := startTUI(t, opts, 200, 50)
			h.wait("Ping Monitor")
			h.press('s')
			h.wait("Stopped. Press 'S' to restart")
			h.press('s') // already stopped: ignored
			h.press('R')
			recvWithin(t, reset, "the reset callback")
			if want := map[bool]int32{true: 1, false: 0}[tt.onStop]; stops.Load() != want {
				t.Errorf("OnStop called %d times, want %d", stops.Load(), want)
			}
			h.quit()
		})
	}
}

// TestRunKeyboardRestart stops and then restarts measuring: OnRestart runs
// and the footer returns to the running key hints.
func TestRunKeyboardRestart(t *testing.T) {
	restarted := make(chan struct{}, 1)
	h := startTUI(t, RunOptions{
		Targets:   []*stats.TargetStats{stats.NewTargetStats("example.com")},
		OnStop:    func() {},
		OnRestart: func() error { restarted <- struct{}{}; return nil },
	}, 200, 50)
	h.wait("Ping Monitor")
	h.press('s')
	h.wait("Stopped. Press 'S' to restart")
	h.press('S')
	recvWithin(t, restarted, "the restart callback")
	h.waitWithout(footerHelpText, "Stopped.")
	h.quit()
}

// TestRunKeyboardReset presses R with every monitor enabled. The reset
// callbacks run after the statistics have been cleared on the same worker,
// so once the last one fires nothing needs polling.
//
// The MTR assertion pins an invariant that rests entirely on this key
// handler: restartMTR on the supervisor side does not reset anything, so
// the only thing clearing per-hop counters on R is the Reset loop here —
// without it, MTR stats would stay uncleared with the whole suite green.
func TestRunKeyboardReset(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.IncSent()
	target.OnSuccess(10*time.Millisecond, 64)
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})
	target.MTR().EnsureLen(2)
	target.MTR().RecordReply(1, "10.0.0.1", "", "", "", 5*time.Millisecond)
	target.PortResults = []*stats.PortCheckResult{{Port: 80, Protocol: "tcp", Status: "Open"}}
	if len(target.MTR().View()) == 0 {
		t.Fatal("fixture did not record any hops")
	}

	resets := make(chan string, 3)
	h := startTUI(t, RunOptions{
		Targets:      []*stats.TargetStats{target},
		TraceEnabled: true, MTREnabled: true, PortEnabled: true,
		OnResetTrace: func() { resets <- "trace" },
		OnResetMTR:   func() { resets <- "mtr" },
		OnResetPort:  func() { resets <- "port" },
	}, 200, 50)
	h.wait("Ping Monitor")
	h.press('R')
	for _, want := range []string{"trace", "mtr", "port"} {
		if got := recvWithin(t, resets, "monitor reset "+want); got != want {
			t.Fatalf("monitor reset %q, want %q", got, want)
		}
	}
	if v := target.GetView(); v.Sent != 0 || v.Recv != 0 {
		t.Errorf("counters not cleared after R: sent=%d recv=%d", v.Sent, v.Recv)
	}
	if hops := target.GetView().TraceHops; len(hops) != 0 {
		t.Errorf("TraceHops not cleared after R: got %v", hops)
	}
	if hops := target.MTR().View(); len(hops) != 0 {
		t.Errorf("MTR hop stats not cleared after R: got %d hops", len(hops))
	}
	h.quit()
}

// TestRunKeyboardScroll moves the selection with the arrow and page keys.
func TestRunKeyboardScroll(t *testing.T) {
	t.Run("a single target has nothing to scroll", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetIP("1.1.1.1")
		h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{target}, SourceIPv4: "10.0.0.1"}, 200, 50)
		h.wait("example.com")
		for _, k := range []tcell.Key{tcell.KeyDown, tcell.KeyUp, tcell.KeyPgDn, tcell.KeyPgUp} {
			h.key(k)
		}
		h.wait("example.com")
		h.quit()
	})

	// With far more targets than rows, only a window of rows is rendered.
	// Paging to the bottom must bring the last host into view and paging
	// back must restore the first, and neither may crash or hang the app.
	t.Run("many targets scroll to the bottom and back", func(t *testing.T) {
		const numTargets = 100
		targets := make([]*stats.TargetStats, numTargets)
		for i := range targets {
			targets[i] = stats.NewTargetStats(fmt.Sprintf("host%03d", i))
			targets[i].SetIP("10.0.0.1")
			targets[i].IncSent()
			targets[i].OnSuccess(10*time.Millisecond, 64)
		}
		h := startTUI(t, RunOptions{Targets: targets}, 200, 50)
		first, last := "host000", fmt.Sprintf("host%03d", numTargets-1)
		frame := h.wait(first)
		if requireAll(last)(frame) {
			t.Fatalf("last host visible before scrolling:\n%s", frame)
		}
		for range numTargets/tableMaxRows + 2 {
			h.key(tcell.KeyPgDn)
		}
		h.wait(last)
		for range numTargets/tableMaxRows + 2 {
			h.key(tcell.KeyPgUp)
		}
		h.waitWithout(first, last)
		h.quit()
	})
}

// TestRunPaneControls checks that Tab, f and z reach the pane controls
// through Run: Tab moves focus to the next pane, which is what f then folds,
// and z maximizes the focused pane until Escape restores the layout.
func TestRunPaneControls(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})
	target.PortResults = []*stats.PortCheckResult{{Port: 443, Protocol: "tcp", Status: "Open"}}
	h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{target}, TraceEnabled: true, PortEnabled: true}, 200, 50)
	h.wait("Port Monitor")

	h.key(tcell.KeyTab) // Ping Monitor -> Traceroute Monitor
	h.press('f')
	folded := h.wait("(f: Expand, z: Maximize)")
	if !requireAll("Traceroute Monitor", "Port Monitor")(folded) {
		t.Fatalf("expected only the Traceroute Monitor folded:\n%s", folded)
	}
	h.press('f')
	h.waitWithout("Traceroute Monitor", "(f: Expand")

	h.press('z')
	maximized := h.waitFrame("the Traceroute Monitor alone", func(f string) bool {
		return requireAll("Traceroute Monitor")(f) && !requireAll("Port Monitor")(f)
	})
	if requireAll("Ping Monitor")(maximized) {
		t.Errorf("maximized pane should hide the Ping Monitor:\n%s", maximized)
	}
	h.key(tcell.KeyEscape)
	h.waitFrame("the full layout", requireAll("Ping Monitor", "Traceroute Monitor", "Port Monitor"))
	h.quit()
}
