package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func graphTargets(n int, host string) []*stats.TargetStats {
	targets := make([]*stats.TargetStats, n)
	for i := range targets {
		targets[i] = stats.NewTargetStats(host)
	}
	return targets
}

func TestProjectDurationsToGraph(t *testing.T) {
	ms := time.Millisecond
	tests := []struct {
		name          string
		data          []time.Duration
		window, width int
		wantLast      time.Duration // right-aligned latest sample, when non-zero
		wantLeftEmpty bool          // slot 0 unfilled: the window is not yet full
		wantAllFilled bool          // a continuous span covers every slot
	}{
		{"partially filled window is right-aligned", []time.Duration{1 * ms, 2 * ms, 3 * ms}, 30, 10, 3 * ms, true, false},
		{"narrow window spans every slot", []time.Duration{1 * ms, 2 * ms, 3 * ms}, 2, 4, 0, false, true},
		{"single sample is right-aligned", []time.Duration{5 * ms}, 1, 3, 5 * ms, true, false},
		{"single sample in a window of one", []time.Duration{5 * ms}, 1, 10, 5 * ms, true, false},
		{"data beyond the window is trimmed to its tail", []time.Duration{1 * ms, 2 * ms, 3 * ms, 4 * ms}, 2, 5, 4 * ms, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, has := projectDurationsToGraph(tt.data, tt.window, tt.width)
			if len(values) != tt.width || len(has) != tt.width {
				t.Fatalf("projection size: got values=%d has=%d, want %d", len(values), len(has), tt.width)
			}
			if tt.wantLast != 0 && (!has[tt.width-1] || values[tt.width-1] != tt.wantLast) {
				t.Errorf("latest sample should be right-aligned: has=%v value=%v, want %v", has[tt.width-1], values[tt.width-1], tt.wantLast)
			}
			if tt.wantLeftEmpty && has[0] {
				t.Errorf("left side should be empty when the window is not yet filled: has=%v", has)
			}
			if tt.wantAllFilled && slices.Contains(has, false) {
				t.Errorf("expected a continuous span: has=%v", has)
			}
		})
	}
}

func TestGraphViewInputHandlerScroll(t *testing.T) {
	tests := []struct {
		name    string
		targets int
		w, h    int
		start   int
		keys    []tcell.Key
		want    []int // scrollRow after each key
	}{
		{"Down then Up moves one row each", 7, 80, 10, 0, []tcell.Key{tcell.KeyDown, tcell.KeyUp}, []int{1, 0}},
		{"PgDn then PgUp pages and clamps", 8, 80, 40, 0, []tcell.Key{tcell.KeyPgDn, tcell.KeyPgUp}, []int{1, 0}},
		{"Up clamps a stale scroll position", 4, 80, 40, 2, []tcell.Key{tcell.KeyUp}, []int{0}},
		{"an unhandled key is a no-op", 1, 80, 20, 0, []tcell.Key{tcell.KeyEnter}, []int{0}},
		{"no targets resets the scroll", 0, 80, 20, 5, []tcell.Key{tcell.KeyDown}, []int{0}},
		{"a zero-size rect resets the scroll", 4, 0, 0, 2, []tcell.Key{tcell.KeyDown}, []int{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraphView(graphTargets(tt.targets, "host"), time.Second)
			g.SetRect(0, 0, tt.w, tt.h)
			g.scrollRow = tt.start
			handler := g.InputHandler()
			for i, key := range tt.keys {
				handler(tcell.NewEventKey(key, 0, 0), func(p tview.Primitive) {})
				if g.scrollRow != tt.want[i] {
					t.Errorf("after key %d scrollRow = %d, want %d", i+1, g.scrollRow, tt.want[i])
				}
			}
		})
	}
}

func TestGraphViewClampScroll(t *testing.T) {
	tests := []struct {
		name                        string
		scrollRow, numRows, visible int
		want                        int
	}{
		{"all rows visible resets to the top", 10, 2, 2, 0},
		{"negative scroll is raised to zero", -1, 5, 2, 0},
		{"fewer rows than visible clamps the negative maximum", 5, 1, 3, 0},
		{"scroll beyond the last page is capped", 10, 5, 2, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraphView(nil, time.Second)
			g.scrollRow = tt.scrollRow
			g.clampScroll(tt.numRows, tt.visible)
			if g.scrollRow != tt.want {
				t.Errorf("scrollRow = %d, want %d", g.scrollRow, tt.want)
			}
		})
	}
}

func TestAdjustPlotArea(t *testing.T) {
	tests := []struct {
		name                     string
		graphY, height           int
		wantPlotY, wantPlotHight int
	}{
		{"normal height", 5, 10, 5, 9},
		// desiredSteps = 0 is raised to 1, then plotY-- is clamped back to graphY.
		{"small height keeps at least one row", 5, 2, 5, 2},
		{"height one is left alone", 3, 1, 3, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plotY, plotHeight := adjustPlotArea(tt.graphY, tt.height)
			if plotY != tt.wantPlotY || plotHeight != tt.wantPlotHight {
				t.Errorf("adjustPlotArea(%d, %d) = (%d, %d), want (%d, %d)",
					tt.graphY, tt.height, plotY, plotHeight, tt.wantPlotY, tt.wantPlotHight)
			}
		})
	}
}

func TestGridStepsForHeight(t *testing.T) {
	tests := []struct {
		height int
		want   [4]int
	}{
		{9, [4]int{2, 4, 6, 8}},
		{1, [4]int{1, 1, 1, 1}}, // totalSteps is clamped to 1
	}
	for _, tt := range tests {
		gy25, gy50, gy75, gy100 := gridStepsForHeight(tt.height)
		if got := [4]int{gy25, gy50, gy75, gy100}; got != tt.want {
			t.Errorf("gridStepsForHeight(%d) = %v, want %v", tt.height, got, tt.want)
		}
	}
}

func TestLayoutGraph(t *testing.T) {
	tests := []struct {
		name   string
		series int
		w, h   int
		check  func(t *testing.T, numCols, numRows, visRows, colW, rowH int)
	}{
		{"min height shows one row of two columns", 6, 80, 9, func(t *testing.T, cols, rows, vis, _, rowH int) {
			if cols != 2 || rows != 3 {
				t.Errorf("cols/rows = %d/%d, want 2/3", cols, rows)
			}
			if vis != 1 {
				t.Errorf("visibleRows = %d, want 1", vis)
			}
			if rowH < 7 {
				t.Errorf("rowHeight = %d, want >= 7", rowH)
			}
		}},
		{"narrow width forces one column", 2, 20, 20, func(t *testing.T, cols, _, _, _, _ int) {
			if cols != 1 {
				t.Errorf("numCols = %d, want 1", cols)
			}
		}},
		{"many targets cap the visible rows", 10, 80, 60, func(t *testing.T, _, _, vis, _, _ int) {
			if vis > graphMaxVisibleRows {
				t.Errorf("visibleRows = %d, want <= %d", vis, graphMaxVisibleRows)
			}
		}},
		{"height 1 clamps the row height to 2", 1, 80, 1, func(t *testing.T, cols, rows, vis, _, rowH int) {
			if cols < 1 || rows < 0 || vis < 0 || rowH < 2 {
				t.Errorf("unexpected layout: cols=%d rows=%d vis=%d rowH=%d", cols, rows, vis, rowH)
			}
		}},
		{"short height with several rows keeps one row of at least 2 lines", 6, 80, 3, func(t *testing.T, _, _, vis, _, rowH int) {
			if vis < 1 || rowH < 2 {
				t.Errorf("visibleRows=%d rowHeight=%d, want >= 1 and >= 2", vis, rowH)
			}
		}},
		{"no series lay out nothing", 0, 80, 24, func(t *testing.T, cols, rows, vis, colW, rowH int) {
			if cols != 1 || rows != 0 || vis != 0 || colW != 0 || rowH != 0 {
				t.Errorf("zero series: unexpected %d %d %d %d %d", cols, rows, vis, colW, rowH)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows, vis, colW, rowH := layoutGraph(tt.series, tt.w, tt.h)
			tt.check(t, cols, rows, vis, colW, rowH)
		})
	}
}

// BenchmarkGraphViewDraw_20Targets_LongHistory tracks GraphView's
// allocations: buildSeries must fetch a windowed history snapshot exactly
// once per target per Draw, since each fetch copies up to historySize
// entries. Run with `go test -bench=. -benchmem`.
func BenchmarkGraphViewDraw_20Targets_LongHistory(b *testing.B) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		b.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(160, 40)

	const numTargets = 20
	targets := make([]*stats.TargetStats, numTargets)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
		for j := range 3000 {
			targets[i].IncSent()
			targets[i].OnSuccess(time.Duration(j%200)*time.Millisecond, 64)
		}
	}

	g := NewGraphView(targets, 200*time.Millisecond)
	g.SetRect(0, 0, 160, 40)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g.Draw(screen)
	}
}

// TestGraphViewDraw renders the graph for a range of target and screen
// shapes and checks the text that must reach the screen.
func TestGraphViewDraw(t *testing.T) {
	const bars = "▁▂▃▄▅▆▇█"
	withRTTs := func(host string, rtts ...time.Duration) *stats.TargetStats {
		target := stats.NewTargetStats(host)
		for _, rtt := range rtts {
			target.IncSent()
			target.OnSuccess(rtt, 64)
		}
		return target
	}
	ms := time.Millisecond
	tests := []struct {
		name     string
		w, h     int
		interval time.Duration
		targets  []*stats.TargetStats
		check    func(t *testing.T, text string)
	}{
		{"no targets draws nothing", 80, 24, time.Second, nil, func(t *testing.T, text string) {
			if strings.TrimSpace(text) != "" {
				t.Errorf("expected a blank screen, got:\n%s", text)
			}
		}},
		{"single target shows its latest RTT and axis", 120, 30, 200 * ms,
			[]*stats.TargetStats{withRTTs("example.com", 10*ms, 20*ms, 30*ms)},
			func(t *testing.T, text string) { requireText(t, text, "example.com", "30ms", "100ms", "0ms") }},
		{"several targets fill two columns", 80, 12, 500 * ms,
			func() []*stats.TargetStats {
				targets := graphTargets(4, "a")
				targets[0].OnSuccess(10*ms, 64)
				return targets
			}(),
			func(t *testing.T, text string) {
				requireText(t, text, "10ms")
				if n := strings.Count(text, "100ms"); n != 2 {
					t.Errorf("axis labels = %d, want one per column (2)", n)
				}
			}},
		{"narrow width still shows the host", 20, 8, time.Second,
			[]*stats.TargetStats{withRTTs("example.com", 10*ms)},
			func(t *testing.T, text string) { requireText(t, text, "example.com") }},
		// An RTT above the 100ms floor rescales the axis instead of overflowing it.
		{"high RTT rescales the axis", 120, 20, 200 * ms,
			[]*stats.TargetStats{withRTTs("example.com", 200*ms, 200*ms, 200*ms, 200*ms, 200*ms, 500*ms)},
			func(t *testing.T, text string) { requireText(t, text, "example.com", "500ms", "1000ms") }},
		// ratio 0.01 is floored to 0.05 so the bar stays visible.
		{"very small RTT still draws a bar", 120, 20, 200 * ms,
			[]*stats.TargetStats{withRTTs("example.com", ms, ms, ms, ms, ms)},
			func(t *testing.T, text string) {
				requireText(t, text, "example.com", "1ms")
				if !strings.ContainsAny(text, bars) {
					t.Errorf("no bar drawn:\n%s", text)
				}
			}},
		// With only two plot rows the floored level rounds to zero and is raised to one.
		{"very small RTT on a short graph still draws a bar", 120, 4, 200 * ms,
			[]*stats.TargetStats{withRTTs("example.com", ms, ms, ms, ms, ms)},
			func(t *testing.T, text string) {
				requireText(t, text, "example.com", "1ms")
				if !strings.ContainsAny(text, bars) {
					t.Errorf("no bar drawn:\n%s", text)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatalf("screen init: %v", err)
			}
			defer screen.Fini()
			screen.SetSize(tt.w, tt.h)
			g := NewGraphView(tt.targets, tt.interval)
			g.SetRect(0, 0, tt.w, tt.h)
			g.Draw(screen)
			tt.check(t, screenText(screen))
		})
	}
}

// TestGraphViewDrawsPortSeries covers the Host Details graph: a single
// target with port checks must lay out one cell per series, not one per
// target, so the port RTTs are drawn and reachable by scrolling.
func TestGraphViewDrawsPortSeries(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.OnSuccess(10*time.Millisecond, 64)
	ports := make([]*stats.PortCheckResult, 4)
	for i := range ports {
		ports[i] = &stats.PortCheckResult{Port: 8000 + i, Protocol: "tcp"}
		ports[i].SetResult("Open", time.Duration(20+i)*time.Millisecond)
	}
	target.SetPortResults(ports)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 18)
	g := NewGraphView([]*stats.TargetStats{target}, time.Second)
	g.includePorts = true
	g.SetRect(0, 0, 80, 18)

	// Five series in two columns: three rows, two of them visible at 18 lines.
	g.Draw(screen)
	requireText(t, screenText(screen), "example.com ", "10ms", "8000/tcp", "20ms", "8001/tcp", "8002/tcp")

	g.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	if g.scrollRow != 1 {
		t.Fatalf("scrollRow = %d, want 1 (the third row of port series)", g.scrollRow)
	}
	g.Draw(screen)
	requireText(t, screenText(screen), "8003/tcp", "23ms")

	// The main graph counts targets only.
	g.includePorts = false
	if got := g.seriesCount(); got != 1 {
		t.Errorf("seriesCount without ports = %d, want 1", got)
	}
}

// requireText fails the test for each of wants missing from text.
func requireText(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("screen missing %q:\n%s", want, text)
		}
	}
}
