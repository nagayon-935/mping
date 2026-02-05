package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestFormatTableError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "extract tail message",
			in:   "write ip 10.70.71.68->8.8.8.8: sendmsg: no route to host",
			want: "no route to host",
		},
		{
			name: "no separator keeps original",
			in:   "Timeout",
			want: "Timeout",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTableError(tt.in); got != tt.want {
				t.Fatalf("formatTableError(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCalcInitialTableErrorWidth_IncludesKnownCandidates(t *testing.T) {
	got := calcInitialTableErrorWidth(nil, "Error", 10)
	want := len("Communication Administratively Prohibited")
	if got < want {
		t.Fatalf("calcInitialTableErrorWidth: got %d, want at least %d", got, want)
	}
}

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

func TestDisplaySourceIPForDst(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		src4     string
		src6     string
		expected string
	}{
		{
			name:     "ipv4 destination uses ipv4 source",
			dst:      "8.8.8.8",
			src4:     "10.0.0.2",
			src6:     "2001:db8::2",
			expected: "10.0.0.2",
		},
		{
			name:     "ipv6 destination with zone uses ipv6 source",
			dst:      "fe80::1%en0",
			src4:     "10.0.0.2",
			src6:     "fe80::2%en0",
			expected: "fe80::2%en0",
		},
		{
			name:     "ipv6 destination falls back to auto when no ipv6 source",
			dst:      "2001:4860:4860::8888",
			src4:     "10.0.0.2",
			src6:     "",
			expected: "Auto",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displaySourceIPForDst(tt.dst, tt.src4, tt.src6)
			if got != tt.expected {
				t.Fatalf("displaySourceIPForDst(%q): got %q, want %q", tt.dst, got, tt.expected)
			}
		})
	}
}

func TestFormatCellText(t *testing.T) {
	got := formatCellText("abcdefghijk", 8, tview.AlignLeft)
	if got != "abcde..." {
		t.Fatalf("left truncate: got %q, want %q", got, "abcde...")
	}

	got = formatCellText("42", 5, tview.AlignRight)
	if got != "   42" {
		t.Fatalf("right pad: got %q, want %q", got, "   42")
	}

	got = formatCellText("abcd", 3, tview.AlignLeft)
	if got != "..." {
		t.Fatalf("small width: got %q, want %q", got, "...")
	}
}

func TestFitWidthsToAvailable(t *testing.T) {
	desired := []int{10, 20, 30}
	min := []int{5, 5, 5}
	max := []int{50, 50, 50}

	widths, ok := fitWidthsToAvailable(desired, min, max, 30)
	if !ok {
		t.Fatalf("fitWidthsToAvailable should succeed")
	}
	total := 0
	for i, w := range widths {
		total += w
		if w < min[i] || w > max[i] {
			t.Fatalf("width out of bounds at %d: %d", i, w)
		}
	}
	if total != 30 {
		t.Fatalf("total width: got %d, want %d", total, 30)
	}

	_, ok = fitWidthsToAvailable(desired, min, max, 10)
	if ok {
		t.Fatalf("fitWidthsToAvailable should fail when available < sum(min)")
	}

	widths, ok = fitWidthsToAvailable(desired, min, max, 80)
	if !ok {
		t.Fatalf("fitWidthsToAvailable should succeed")
	}
	total = 0
	for _, w := range widths {
		total += w
	}
	if total != 80 {
		t.Fatalf("total width: got %d, want %d", total, 80)
	}
}

func TestTruncateToDisplayWidth(t *testing.T) {
	got := truncateToDisplayWidth("abcdef", 4)
	if got != "a..." {
		t.Fatalf("truncate: got %q, want %q", got, "a...")
	}
	got = truncateToDisplayWidth("ab", 2)
	if got != "ab" {
		t.Fatalf("short string: got %q", got)
	}
}

func TestCalcLossRate(t *testing.T) {
	view := stats.TargetView{Recv: 80, Loss: 20}
	if got := calcLossRate(view); got < 19.9 || got > 20.1 {
		t.Fatalf("loss rate: got %v", got)
	}
}

func TestFormatLossAgo(t *testing.T) {
	if got := formatLossAgo(time.Time{}); got != "-" {
		t.Fatalf("zero time: got %q", got)
	}
	now := time.Now().Add(-2 * time.Second)
	got := formatLossAgo(now)
	if !strings.Contains(got, "ago") {
		t.Fatalf("expected ago suffix: got %q", got)
	}
}

func TestBuildCompactLayout(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.OnSuccess(12*time.Millisecond, 64)
	target.SetIfaceMTU(1500)
	layout := buildCompactLayout([]*stats.TargetStats{target}, 56, "10.0.0.2", "", 20)
	if len(layout.rows) != 2 {
		t.Fatalf("rows: got %d", len(layout.rows))
	}
	if len(layout.headers) != 4 || len(layout.aligns) != 4 {
		t.Fatalf("headers/aligns size mismatch")
	}
	if layout.max[3] != 20 {
		t.Fatalf("error max width: got %d", layout.max[3])
	}
}

func TestBuildFullColumns(t *testing.T) {
	view := stats.TargetView{
		Host:   "example.com",
		IP:     "1.1.1.1",
		Recv:   10,
		Loss:   2,
		LastRTT: 12 * time.Millisecond,
		AvgRTT:  10 * time.Millisecond,
		Jitter:  2 * time.Millisecond,
		IfaceMTU: 1500,
		LastTTL:  64,
	}
	cols, src, rate := buildFullColumns(view, "10.0.0.2", "", 56)
	if src != "10.0.0.2" {
		t.Fatalf("src: got %q", src)
	}
	if rate <= 0 {
		t.Fatalf("loss rate: got %v", rate)
	}
	if len(cols) != 14 {
		t.Fatalf("cols len: got %d", len(cols))
	}
}

func TestColorHelpers(t *testing.T) {
	if lossColorForRate(10, tcell.ColorRed) != tcell.ColorGreen {
		t.Fatal("expected green for low loss")
	}
	if lossColorForRate(50, tcell.ColorRed) != tcell.ColorOrange {
		t.Fatal("expected orange for mid loss")
	}
	if lossColorForRate(90, tcell.ColorRed) != tcell.ColorRed {
		t.Fatal("expected red for high loss")
	}

	if rttColorForRTT(0, tcell.ColorRed) != tcell.ColorWhite {
		t.Fatal("expected white for zero rtt")
	}
	if rttColorForRTT(10*time.Millisecond, tcell.ColorRed) != tcell.ColorGreen {
		t.Fatal("expected green for low rtt")
	}
	if jitterColorForJitter(0, tcell.ColorRed) != tcell.ColorWhite {
		t.Fatal("expected white for zero jitter")
	}
	if jitterColorForJitter(20*time.Millisecond, tcell.ColorRed) != tcell.ColorOrange {
		t.Fatal("expected orange for mid jitter")
	}
}

func TestBuildFullRowCells(t *testing.T) {
	cols := []string{"h", "s", "d", "1", "2", "10.0%", "1ms", "1ms", "1ms", "56", "1500", "64", "err", "1s ago"}
	widths := make([]int, len(cols))
	for i := range widths {
		widths[i] = 5
	}
	aligns := make([]int, len(cols))
	cells := buildFullRowCells(cols, widths, aligns, 90.0, 300*time.Millisecond, 60*time.Millisecond, tcell.ColorRed, tcell.ColorWhite)
	if len(cells) != len(cols) {
		t.Fatalf("cells len mismatch")
	}
	if cells[12].Color == tcell.ColorWhite {
		t.Fatalf("expected error cell colored")
	}
}

func TestBuildCompactRowCells(t *testing.T) {
	values := []string{"host", "path", "stat", "err"}
	widths := []int{4, 4, 4, 4}
	aligns := []int{tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignLeft}
	cells := buildCompactRowCells(values, widths, aligns, tcell.ColorRed, tcell.ColorWhite)
	if len(cells) != 4 {
		t.Fatalf("cells len mismatch")
	}
	if cells[3].Text == "" {
		t.Fatalf("expected error cell text")
	}
}

func TestAppendErrorLog(t *testing.T) {
	view := tview.NewTextView()
	logs := []string{}
	appendErrorLog(&logs, view, "one")
	appendErrorLog(&logs, view, "two")
	if len(logs) != 2 {
		t.Fatalf("logs len: got %d", len(logs))
	}
	if !strings.Contains(view.GetText(false), "two") {
		t.Fatalf("expected latest log in text")
	}
}

func TestTTLAndMTUString(t *testing.T) {
	if ttlString(0) != "-" {
		t.Fatal("expected '-' for ttl 0")
	}
	if ttlString(64) != "64" {
		t.Fatal("expected ttl string")
	}
	if mtuString(0) != "-" {
		t.Fatal("expected '-' for mtu 0")
	}
	if mtuString(1500) != "1500" {
		t.Fatal("expected mtu string")
	}
}

func TestNormalizeWriteIP(t *testing.T) {
	msg := normalizeWriteIP("write ip 0.0.0.0->1.1.1.1: x", "10.0.0.2")
	if !strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("expected replaced source ip, got %q", msg)
	}
	msg = normalizeWriteIP("write ip 0.0.0.0->1.1.1.1: x", "Auto")
	if strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("unexpected replacement: %q", msg)
	}
	msg = normalizeWriteIP("other error", "10.0.0.2")
	if msg != "other error" {
		t.Fatalf("unexpected change: %q", msg)
	}
}

func TestBuildErrorLogMessage(t *testing.T) {
	view := stats.TargetView{Host: "example.com"}
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msg := buildErrorLogMessage(view, "10.0.0.2", "write ip 0.0.0.0->1.1.1.1: x", ts)
	if !strings.Contains(msg, "example.com") || !strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("unexpected msg: %q", msg)
	}
}

func TestUpdateAlertState(t *testing.T) {
	view := stats.TargetView{
		Host:    "example.com",
		LastRTT: 300 * time.Millisecond,
		Jitter:  60 * time.Millisecond,
	}
	state, msgs := updateAlertState(view, "10.0.0.2", 90.0, time.Now(), alertFlags{})
	if !state.lossRed || !state.rttRed || !state.jitterRed {
		t.Fatalf("expected alert flags set: %+v", state)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	clearView := stats.TargetView{Host: "example.com"}
	state2, msgs2 := updateAlertState(clearView, "10.0.0.2", 0.0, time.Now(), state)
	if state2.lossRed || state2.rttRed || state2.jitterRed {
		t.Fatalf("expected flags cleared: %+v", state2)
	}
	if len(msgs2) != 0 {
		t.Fatalf("expected no messages, got %d", len(msgs2))
	}
}

func TestWrapRoute(t *testing.T) {
	if got := wrapRoute(nil, 10); got != "-" {
		t.Fatalf("empty hops: got %q", got)
	}
	hops := []string{"a", "b", "c"}
	if got := wrapRoute(hops, 20); !strings.Contains(got, "a -> b -> c") {
		t.Fatalf("wrapRoute: got %q", got)
	}
}

func TestWrapRouteLines(t *testing.T) {
	lines := wrapRouteLines([]string{"a", "b", "c"}, 4)
	if len(lines) < 2 {
		t.Fatalf("expected wrapped lines, got %v", lines)
	}
}

func TestFormatRTT(t *testing.T) {
	if got := formatRTT(0); got != "-" {
		t.Fatalf("expected '-', got %q", got)
	}
	if got := formatRTT(10 * time.Millisecond); !strings.Contains(got, "ms") {
		t.Fatalf("expected ms, got %q", got)
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
	totalSteps, gy25, gy50, gy75, gy100 := gridStepsForHeight(9)
	if totalSteps != 8 || gy25 != 2 || gy50 != 4 || gy75 != 6 || gy100 != 8 {
		t.Fatalf("unexpected steps: total=%d 25=%d 50=%d 75=%d 100=%d",
			totalSteps, gy25, gy50, gy75, gy100)
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

func TestRunWithSimulationScreen(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	newApplication = func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		app.SetScreen(screen)
		screen.SetSize(80, 24)
		go func() {
			time.Sleep(30 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("example.com")
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(done)
	}()

	err := Run([]*stats.TargetStats{target}, 50*time.Millisecond, done, "", "", 56, nil, false, nil, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
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
