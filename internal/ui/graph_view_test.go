package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestProjectDurationsToGraph(t *testing.T) {
	data := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
	}

	values, has := projectDurationsToGraph(data, 30, 10)
	if len(values) != 10 || len(has) != 10 {
		t.Fatalf("projection size: got values=%d has=%d, want 10", len(values), len(has))
	}
	if has[9] != true || values[9] != data[len(data)-1] {
		t.Fatalf("latest sample should be right-aligned: has=%v value=%v", has[9], values[9])
	}
	if has[0] {
		t.Fatalf("left side should be empty when window is not yet filled: has=%v", has)
	}

	values2, has2 := projectDurationsToGraph(data, 2, 4)
	if len(values2) != 4 || len(has2) != 4 {
		t.Fatalf("projection2 size: got values=%d has=%d, want 4", len(values2), len(has2))
	}
	if !has2[0] || !has2[1] || !has2[2] || !has2[3] {
		t.Fatalf("expected continuous span in narrow window: has=%v", has2)
	}
}

func TestProjectDurationsToGraphSinglePoint(t *testing.T) {
	data := []time.Duration{5 * time.Millisecond}
	values, has := projectDurationsToGraph(data, 1, 3)
	if len(values) != 3 || len(has) != 3 {
		t.Fatalf("size mismatch")
	}
	if !has[2] || values[2] != data[0] {
		t.Fatalf("expected right-aligned value")
	}
}

func TestGraphViewInputHandlerScroll(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("a"),
		stats.NewTargetStats("b"),
		stats.NewTargetStats("c"),
		stats.NewTargetStats("d"),
		stats.NewTargetStats("e"),
		stats.NewTargetStats("f"),
		stats.NewTargetStats("g"),
	}
	g := NewGraphView(targets, 1*time.Second)
	g.SetRect(0, 0, 80, 10)

	handler := g.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
	if g.scrollRow == 0 {
		t.Fatalf("expected scrollRow to change")
	}
	handler(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(p tview.Primitive) {})
	if g.scrollRow < 0 {
		t.Fatalf("scrollRow should not be negative")
	}
}

func TestGraphViewClampScroll(t *testing.T) {
	g := NewGraphView(nil, 1*time.Second)
	g.scrollRow = 10
	g.clampScroll(2, 2)
	if g.scrollRow != 0 {
		t.Fatalf("expected scrollRow clamped to 0, got %d", g.scrollRow)
	}
	g.scrollRow = -1
	g.clampScroll(5, 2)
	if g.scrollRow != 0 {
		t.Fatalf("expected scrollRow >=0, got %d", g.scrollRow)
	}
}

func TestAdjustPlotArea(t *testing.T) {
	plotY, plotHeight := adjustPlotArea(5, 10)
	if plotHeight != 9 {
		t.Fatalf("plotHeight: got %d, want 9", plotHeight)
	}
	if plotY != 5 {
		t.Fatalf("plotY: got %d, want 5", plotY)
	}
}

func TestGridStepsForHeight(t *testing.T) {
	gy25, gy50, gy75, gy100 := gridStepsForHeight(9)
	if gy25 != 2 || gy50 != 4 || gy75 != 6 || gy100 != 8 {
		t.Fatalf("unexpected steps: 25=%d 50=%d 75=%d 100=%d",
			gy25, gy50, gy75, gy100)
	}
}

func TestGraphViewLayoutMinHeight(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("a"),
		stats.NewTargetStats("b"),
		stats.NewTargetStats("c"),
		stats.NewTargetStats("d"),
		stats.NewTargetStats("e"),
		stats.NewTargetStats("f"),
	}
	g := NewGraphView(targets, 1*time.Second)
	numCols, numRowsTotal, visibleRows, _, rowHeight := g.layout(80, 9)
	if numCols != 2 || numRowsTotal != 3 {
		t.Fatalf("layout cols/rows: got cols=%d rows=%d", numCols, numRowsTotal)
	}
	if visibleRows != 1 {
		t.Fatalf("visibleRows: got %d, want 1", visibleRows)
	}
	if rowHeight < 7 {
		t.Fatalf("rowHeight: got %d, want >=7", rowHeight)
	}
}

// BenchmarkGraphViewDraw_20Targets_LongHistory is the P2 fix's
// alloc-reduction proof for GraphView: before the fix, each target's
// history was fetched (and fully copied, up to historySize entries) 3
// separate times per Draw call (seriesLabel, windowMax's own snapshot
// call, and the per-cell draw loop's snapshot call). After the fix,
// buildSeries fetches a windowed snapshot exactly once per target per
// Draw. Run with `go test -bench=. -benchmem`.
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
		for j := 0; j < 3000; j++ {
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

func TestGraphViewDraw_EmptyTargets(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 24)

	g := NewGraphView(nil, 1*time.Second)
	g.SetRect(0, 0, 80, 24)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Draw panicked: %v", r)
		}
	}()
	g.Draw(screen)
}

func TestGraphViewDraw_SingleTarget(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(120, 30)

	target := stats.NewTargetStats("example.com")
	target.OnSuccess(10*time.Millisecond, 64)
	target.OnSuccess(20*time.Millisecond, 64)
	target.OnSuccess(30*time.Millisecond, 64)

	g := NewGraphView([]*stats.TargetStats{target}, 200*time.Millisecond)
	g.SetRect(0, 0, 120, 30)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Draw panicked: %v", r)
		}
	}()
	g.Draw(screen)
}

func TestGraphViewDraw_MultiTargets(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 12)

	targets := []*stats.TargetStats{
		stats.NewTargetStats("a"),
		stats.NewTargetStats("b"),
		stats.NewTargetStats("c"),
		stats.NewTargetStats("d"),
	}
	targets[0].OnSuccess(10*time.Millisecond, 64)

	g := NewGraphView(targets, 500*time.Millisecond)
	g.SetRect(0, 0, 80, 12)

	g.Draw(screen)
}

func TestGraphViewDraw_NarrowWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(20, 8)

	target := stats.NewTargetStats("example.com")
	target.OnSuccess(10*time.Millisecond, 64)
	g := NewGraphView([]*stats.TargetStats{target}, 1*time.Second)
	g.SetRect(0, 0, 20, 8)
	g.Draw(screen)
}

func TestGraphViewDraw_HeaderText(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 10)

	target := stats.NewTargetStats("example.com")
	target.OnSuccess(10*time.Millisecond, 64)

	g := NewGraphView([]*stats.TargetStats{target}, 1*time.Second)
	g.SetRect(0, 0, 80, 10)

	g.Draw(screen)

	row := screenRowString(screen, 0, 80)
	if !strings.Contains(row, "example.com") {
		t.Fatalf("header missing hostname: %q", row)
	}
	if !strings.Contains(row, "10ms") {
		t.Fatalf("header missing RTT: %q", row)
	}
}

func screenRowString(screen tcell.Screen, y, width int) string {
	var b strings.Builder
	for x := 0; x < width; x++ {
		r, _, _, _ := screen.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ---- InputHandler: PgUp and PgDn ----

func TestGraphViewInputHandlerPgUpPgDn(t *testing.T) {
	targets := make([]*stats.TargetStats, 8)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
	}
	g := NewGraphView(targets, time.Second)
	g.SetRect(0, 0, 80, 40)

	handler := g.InputHandler()

	// PgDn should increase scroll by 3
	before := g.scrollRow
	handler(tcell.NewEventKey(tcell.KeyPgDn, 0, 0), func(p tview.Primitive) {})
	if g.scrollRow <= before {
		t.Errorf("PgDn: scrollRow should increase, got %d (was %d)", g.scrollRow, before)
	}

	// PgUp should decrease scroll
	before = g.scrollRow
	handler(tcell.NewEventKey(tcell.KeyPgUp, 0, 0), func(p tview.Primitive) {})
	if g.scrollRow >= before {
		t.Errorf("PgUp: scrollRow should decrease, got %d (was %d)", g.scrollRow, before)
	}
}

func TestGraphViewInputHandlerDefault(t *testing.T) {
	g := NewGraphView([]*stats.TargetStats{stats.NewTargetStats("a")}, time.Second)
	g.SetRect(0, 0, 80, 20)
	handler := g.InputHandler()
	before := g.scrollRow
	// Unhandled key should be a no-op (returns early)
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})
	if g.scrollRow != before {
		t.Errorf("unhandled key: scrollRow changed unexpectedly")
	}
}

// ---- clampScroll: numRowsTotal < visibleRows → maxScroll clamped from negative ----

func TestClampScroll_NegativeMaxScroll(t *testing.T) {
	g := NewGraphView(nil, time.Second)
	g.scrollRow = 5
	// numRowsTotal=1 < visibleRows=3 → maxScroll = -2 → clamped to 0 → scrollRow = 0
	g.clampScroll(1, 3)
	if g.scrollRow != 0 {
		t.Errorf("expected scrollRow=0 after negative maxScroll, got %d", g.scrollRow)
	}
}

func TestClampScroll_ScrollRowAboveMax(t *testing.T) {
	g := NewGraphView(nil, time.Second)
	g.scrollRow = 10
	// numRowsTotal=5, visibleRows=2 → maxScroll=3; scrollRow(10) > maxScroll(3) → clamp
	g.clampScroll(5, 2)
	if g.scrollRow != 3 {
		t.Errorf("expected scrollRow=3, got %d", g.scrollRow)
	}
}

// ---- adjustPlotArea: small height ----

func TestAdjustPlotArea_SmallHeight(t *testing.T) {
	// height=2: desiredSteps=0 < 1 → clamped to 1; then plotY-- → plotY < graphY → clamped
	plotY, plotHeight := adjustPlotArea(5, 2)
	if plotHeight < 1 {
		t.Errorf("plotHeight should be >= 1, got %d", plotHeight)
	}
	if plotY < 5 {
		t.Errorf("plotY should be >= graphY(5), got %d", plotY)
	}
}

func TestAdjustPlotArea_HeightOne(t *testing.T) {
	// height=1: plotHeight=1, first if (plotHeight > 1) is false → no adjustment
	plotY, plotHeight := adjustPlotArea(3, 1)
	if plotY != 3 || plotHeight != 1 {
		t.Errorf("expected plotY=3 plotHeight=1, got plotY=%d plotHeight=%d", plotY, plotHeight)
	}
}

// ---- gridStepsForHeight: height=1 → totalSteps (== gy100) clamped to 1 ----

func TestGridStepsForHeight_One(t *testing.T) {
	_, _, _, gy100 := gridStepsForHeight(1)
	if gy100 != 1 {
		t.Errorf("expected gy100=1 for height=1, got %d", gy100)
	}
}

// ---- layout: narrow width, many targets, small height ----

func TestGraphViewLayout_NarrowWidth(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("a"), stats.NewTargetStats("b"),
	}
	g := NewGraphView(targets, time.Second)
	// Width too narrow for 2 columns → forced to 1 column
	numCols, _, _, _, _ := g.layout(20, 20)
	if numCols != 1 {
		t.Errorf("expected numCols=1 for narrow width, got %d", numCols)
	}
}

func TestGraphViewLayout_ManyTargets(t *testing.T) {
	targets := make([]*stats.TargetStats, 10)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
	}
	g := NewGraphView(targets, time.Second)
	// 10 targets with 2 cols → numRowsTotal=5 > graphMaxVisibleRows(3) → cap visibleRows
	_, _, visibleRows, _, _ := g.layout(80, 60)
	if visibleRows > graphMaxVisibleRows {
		t.Errorf("visibleRows should be capped at %d, got %d", graphMaxVisibleRows, visibleRows)
	}
}

func TestGraphViewLayout_SmallHeight(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("a")}
	g := NewGraphView(targets, time.Second)
	// height=1 → initial rowHeight=1 < 2 → clamped to 2
	numCols, numRows, visRows, _, rowH := g.layout(80, 1)
	if numCols < 1 || numRows < 0 || visRows < 0 || rowH < 2 {
		t.Errorf("unexpected layout values: cols=%d rows=%d vis=%d rowH=%d",
			numCols, numRows, visRows, rowH)
	}
}

func TestGraphViewLayout_ZeroTargets(t *testing.T) {
	g := NewGraphView(nil, time.Second)
	numCols, numRows, visRows, colW, rowH := g.layout(80, 24)
	if numCols != 1 || numRows != 0 || visRows != 0 || colW != 0 || rowH != 0 {
		t.Errorf("zero targets: unexpected %d %d %d %d %d", numCols, numRows, visRows, colW, rowH)
	}
}

// ---- projectDurationsToGraph: windowPoints=1 ----

func TestProjectDurationsToGraph_WindowOne(t *testing.T) {
	data := []time.Duration{5 * time.Millisecond}
	values, has := projectDurationsToGraph(data, 1, 10)
	if len(values) != 10 || len(has) != 10 {
		t.Fatalf("unexpected size: values=%d has=%d", len(values), len(has))
	}
	if !has[9] || values[9] != 5*time.Millisecond {
		t.Errorf("expected last slot filled: has[9]=%v values[9]=%v", has[9], values[9])
	}
}

// ---- InputHandler: zero-size rect → early return; zero targets → scrollRow=0 ----

func TestGraphViewInputHandlerNoRect(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("a")}
	g := NewGraphView(targets, time.Second)
	// Do NOT call SetRect → inner rect is (0,0,0,0) → width=0, height=0
	handler := g.InputHandler()
	// Should return early after key handling (width <= 0 || height <= 0)
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
	// No assert needed: just verifying no panic and early-return path is hit
}

func TestGraphViewInputHandlerNoTargets(t *testing.T) {
	// GraphView with no targets → layout returns visibleRows=0 → scrollRow reset to 0
	g := NewGraphView(nil, time.Second)
	g.SetRect(0, 0, 80, 20)
	g.scrollRow = 5
	handler := g.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
	if g.scrollRow != 0 {
		t.Errorf("expected scrollRow=0 for no-targets, got %d", g.scrollRow)
	}
}

// ---- layout: small height, loop rowHeight < 2 ----

func TestGraphViewLayout_SmallHeightMultiRows(t *testing.T) {
	targets := make([]*stats.TargetStats, 6)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
	}
	g := NewGraphView(targets, time.Second)
	// height=3, visibleRows starts at 3, loop will reduce visibleRows and set rowHeight < 2
	_, _, visRows, _, rowH := g.layout(80, 3)
	if visRows < 1 {
		t.Errorf("visibleRows should be at least 1, got %d", visRows)
	}
	if rowH < 2 {
		t.Errorf("rowHeight should be at least 2, got %d", rowH)
	}
}

// ---- projectDurationsToGraph: data > windowPoints → trim ----

func TestProjectDurationsToGraph_DataExceedsWindow(t *testing.T) {
	data := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
	}
	// windowPoints=2 < len(data)=4 → trim to last 2
	values, has := projectDurationsToGraph(data, 2, 5)
	if len(values) != 5 || len(has) != 5 {
		t.Fatalf("unexpected size: values=%d has=%d", len(values), len(has))
	}
}

// ---- Draw: RTT > 100ms (v > yMax cap) ----

func TestGraphViewDraw_HighRTT(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(120, 20)

	target := stats.NewTargetStats("example.com")
	// Add RTT values > 100ms to trigger v > yMax cap branch
	for i := 0; i < 5; i++ {
		target.IncSent()
		target.OnSuccess(200*time.Millisecond, 64)
	}
	target.IncSent()
	target.OnSuccess(500*time.Millisecond, 64)

	g := NewGraphView([]*stats.TargetStats{target}, 200*time.Millisecond)
	g.SetRect(0, 0, 120, 20)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Draw panicked: %v", r)
		}
	}()
	g.Draw(screen)
}

// ---- Draw: very small RTT (ratio < 0.05 floor) ----

func TestGraphViewDraw_SmallRTT(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(120, 20)

	target := stats.NewTargetStats("example.com")
	// Add very small RTT (1ms) to trigger ratio < 0.05 floor branch
	for i := 0; i < 5; i++ {
		target.IncSent()
		target.OnSuccess(1*time.Millisecond, 64)
	}

	g := NewGraphView([]*stats.TargetStats{target}, 200*time.Millisecond)
	g.SetRect(0, 0, 120, 20)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Draw panicked: %v", r)
		}
	}()
	g.Draw(screen)
}

// ---- InputHandler: KeyUp with valid rect and targets ----

func TestGraphViewInputHandler_KeyUp(t *testing.T) {
	targets := make([]*stats.TargetStats, 4)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
	}
	g := NewGraphView(targets, time.Second)
	g.SetRect(0, 0, 80, 40)
	g.scrollRow = 2 // start scrolled down

	handler := g.InputHandler()
	before := g.scrollRow
	handler(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(p tview.Primitive) {})
	if g.scrollRow >= before {
		t.Errorf("KeyUp: scrollRow should decrease, got %d (was %d)", g.scrollRow, before)
	}
}

// ---- Draw: very small height (4) with small RTT covers totalLevels=1 ----

func TestGraphViewDraw_SmallHeightSmallRTT(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(120, 4)

	target := stats.NewTargetStats("example.com")
	// Very small RTT = 1ms → ratio=0.01 < 0.05 → floored to 0.05
	// With plotHeight=2: totalLevels = int(0.05*2*8)=0 → totalLevels=1 branch
	for i := 0; i < 5; i++ {
		target.IncSent()
		target.OnSuccess(1*time.Millisecond, 64)
	}

	g := NewGraphView([]*stats.TargetStats{target}, 200*time.Millisecond)
	g.SetRect(0, 0, 120, 4)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Draw panicked: %v", r)
		}
	}()
	g.Draw(screen)
}
