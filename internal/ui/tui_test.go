package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestInferInitialTTL(t *testing.T) {
	tests := []struct {
		name    string
		lastTTL int
		want    string
	}{
		{"zero", 0, "-"},
		{"negative", -1, "-"},
		{"linux one hop away", 63, "64"},
		{"linux at boundary", 64, "64"},
		{"windows one hop above linux boundary", 65, "128"},
		{"windows one hop away", 127, "128"},
		{"windows at boundary", 128, "128"},
		{"network device one hop above windows boundary", 129, "255"},
		{"network device at max", 255, "255"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferInitialTTL(tt.lastTTL); got != tt.want {
				t.Fatalf("inferInitialTTL(%d) = %q, want %q", tt.lastTTL, got, tt.want)
			}
		})
	}
}

func TestHopCountString(t *testing.T) {
	tests := []struct {
		name string
		hops []string
		want string
	}{
		{"nil", nil, "-"},
		{"empty", []string{}, "-"},
		{"one hop", []string{"1.1.1.1"}, "1"},
		{"three hops", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}, "3"},
		{"with unreachable", []string{"1.1.1.1", "*", "8.8.8.8"}, "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hopCountString(tt.hops); got != tt.want {
				t.Fatalf("hopCountString(%v) = %q, want %q", tt.hops, got, tt.want)
			}
		})
	}
}

func TestWrapHops(t *testing.T) {
	tests := []struct {
		name     string
		hops     []string
		maxWidth int
		want     []string
	}{
		{"nil hops", nil, 80, nil},
		{"empty hops", []string{}, 80, nil},
		{"zero maxWidth", []string{"1.1.1.1"}, 0, nil},
		{
			"all fit on one line",
			[]string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			80,
			[]string{"1.1.1.1 -> 2.2.2.2 -> 3.3.3.3"},
		},
		{
			// "1.1.1.1 -> 2.2.2.2" = 18 chars fits, adding " -> 3.3.3.3" = 29 > 20
			"wraps at hop boundary",
			[]string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			20,
			[]string{"1.1.1.1 -> 2.2.2.2", "3.3.3.3"},
		},
		{
			// maxWidth too narrow for even two hops together: each hop on own line
			"each hop on its own line",
			[]string{"1.1.1.1", "2.2.2.2"},
			7,
			[]string{"1.1.1.1", "2.2.2.2"},
		},
		{
			// single hop wider than maxWidth is still returned as-is
			"single hop wider than maxWidth",
			[]string{"192.168.100.200"},
			5,
			[]string{"192.168.100.200"},
		},
		{
			// first hop wider than maxWidth, second hop fits its own line
			"long first hop forces each on own line",
			[]string{"192.168.100.200", "10.0.0.1"},
			5,
			[]string{"192.168.100.200", "10.0.0.1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapHops(tt.hops, tt.maxWidth)
			if len(got) != len(tt.want) {
				t.Fatalf("wrapHops(%v, %d) = %v, want %v", tt.hops, tt.maxWidth, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("wrapHops(%v, %d)[%d] = %q, want %q", tt.hops, tt.maxWidth, i, got[i], tt.want[i])
				}
			}
		})
	}
}

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
		Host:      "example.com",
		IP:        "1.1.1.1",
		ASN:       "AS15169",
		DNSServer: "8.8.8.8",
		Recv:      10,
		Loss:      2,
		LastRTT:   12 * time.Millisecond,
		AvgRTT:    10 * time.Millisecond,
		Jitter:    2 * time.Millisecond,
		IfaceMTU:  1500,
		LastTTL:   64,
	}

	// Case 1: dnsEnabled = false, asnEnabled = true
	cols, src, rate := buildFullColumns(view, "10.0.0.2", "", 56, true, false)
	if src != "10.0.0.2" {
		t.Fatalf("src: got %q", src)
	}
	if rate <= 0 {
		t.Fatalf("loss rate: got %v", rate)
	}
	if len(cols) != 14 {
		t.Fatalf("cols len: got %d", len(cols))
	}
	// Dst IP should include hostname when host != IP
	if cols[1] != "example.com (1.1.1.1)" {
		t.Fatalf("dst ip: got %q", cols[1])
	}
	if cols[2] != "AS15169" {
		t.Fatalf("asn: got %q", cols[2])
	}

	// Case 2: dnsEnabled = true, asnEnabled = true
	cols2, _, _ := buildFullColumns(view, "10.0.0.2", "", 56, true, true)
	if len(cols2) != 15 {
		t.Fatalf("cols len (dns enabled): got %d", len(cols2))
	}
	if cols2[1] != "example.com (1.1.1.1)" {
		t.Fatalf("dst ip: got %q", cols2[1])
	}
	if cols2[2] != "8.8.8.8" {
		t.Fatalf("dns: got %q, expected 8.8.8.8", cols2[2])
	}
	if cols2[3] != "AS15169" {
		t.Fatalf("asn: got %q", cols2[3])
	}
}

func TestColorHelpers(t *testing.T) {
	if lossColorForRate(10) != tcell.ColorGreen {
		t.Fatal("expected green for low loss")
	}
	if lossColorForRate(50) != tcell.ColorOrange {
		t.Fatal("expected orange for mid loss")
	}
	if lossColorForRate(90) != vividRed {
		t.Fatal("expected red for high loss")
	}

	if rttColorForRTT(0) != tcell.ColorWhite {
		t.Fatal("expected white for zero rtt")
	}
	if rttColorForRTT(10*time.Millisecond) != tcell.ColorGreen {
		t.Fatal("expected green for low rtt")
	}
	if jitterColorForJitter(0) != tcell.ColorWhite {
		t.Fatal("expected white for zero jitter")
	}
	if jitterColorForJitter(20*time.Millisecond) != tcell.ColorOrange {
		t.Fatal("expected orange for mid jitter")
	}
}

func TestBuildFullRowCells(t *testing.T) {
	// Case 1: dnsEnabled = false, asnEnabled = true
	cols := []string{"s", "d", "AS123", "1", "2", "10.0%", "1ms", "1ms", "1ms", "56", "1500", "64", "err", "1s ago"}
	widths := make([]int, len(cols))
	for i := range widths {
		widths[i] = 5
	}
	aligns := make([]int, len(cols))
	cells := buildFullRowCells(cols, widths, aligns, 90.0, 300*time.Millisecond, 60*time.Millisecond, tcell.ColorWhite, true, false)
	if len(cells) != len(cols) {
		t.Fatalf("cells len mismatch")
	}
	if cells[12].Color == tcell.ColorWhite {
		t.Fatalf("expected error cell colored")
	}

	// Case 2: dnsEnabled = true, asnEnabled = true
	cols2 := []string{"s", "d", "dns", "AS123", "1", "2", "10.0%", "1ms", "1ms", "1ms", "56", "1500", "64", "err", "1s ago"}
	widths2 := make([]int, len(cols2))
	for i := range widths2 {
		widths2[i] = 5
	}
	aligns2 := make([]int, len(cols2))
	cells2 := buildFullRowCells(cols2, widths2, aligns2, 90.0, 300*time.Millisecond, 60*time.Millisecond, tcell.ColorWhite, true, true)
	if len(cells2) != len(cols2) {
		t.Fatalf("cells len mismatch (dns enabled)")
	}
}

func TestBuildCompactRowCells(t *testing.T) {
	values := []string{"host", "path", "stat", "err"}
	widths := []int{4, 4, 4, 4}
	aligns := []int{tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignLeft}
	cells := buildCompactRowCells(values, widths, aligns, tcell.ColorWhite)
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

	err := Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, DoneCh: done, PacketSize: 56})
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

func TestPortServiceName(t *testing.T) {
	tests := []struct {
		port     int
		protocol string
		want     string
	}{
		{443, "tcp", "HTTPS"},
		{80, "tcp", "HTTP"},
		{53, "tcp", "DNS"},
		{53, "udp", "DNS"},
		{22, "tcp", "SSH"},
		{67, "udp", "DHCP"},
		{123, "udp", "NTP"},
		{3306, "tcp", "MySQL"},
		{5432, "tcp", "PostgreSQL"},
		{6379, "tcp", "Redis"},
		{27017, "tcp", "MongoDB"},
		// Protocol mismatch → Unknown
		{80, "udp", "Unknown"},
		{443, "udp", "Unknown"},
		// Unknown port → Unknown
		{9999, "tcp", "Unknown"},
		{0, "tcp", "Unknown"},
	}
	for _, tt := range tests {
		got := portServiceName(tt.port, tt.protocol)
		if got != tt.want {
			t.Errorf("portServiceName(%d, %q): got %q, want %q", tt.port, tt.protocol, got, tt.want)
		}
	}
}

// ---- rttColorForRTT / jitterColorForJitter missing branches ----

func TestRttColorForRTT_Orange(t *testing.T) {
	// 50ms < rtt <= 200ms → ColorOrange
	got := rttColorForRTT(100 * time.Millisecond)
	if got != tcell.ColorOrange {
		t.Errorf("expected ColorOrange for 100ms rtt, got %v", got)
	}
}

func TestJitterColorForJitter_Green(t *testing.T) {
	// 0 < jitter <= 10ms → ColorGreen
	got := jitterColorForJitter(5 * time.Millisecond)
	if got != tcell.ColorGreen {
		t.Errorf("expected ColorGreen for 5ms jitter, got %v", got)
	}
}

// ---- appendErrorLog truncation ----

func TestAppendErrorLog_Truncation(t *testing.T) {
	view := tview.NewTextView()
	logs := make([]string, 0)
	// Fill to exactly 1000 entries
	for i := 0; i < 1000; i++ {
		logs = append(logs, "entry")
	}
	// Adding one more should trigger truncation (remove oldest entry)
	appendErrorLog(&logs, view, "new-entry")
	if len(logs) != 1000 {
		t.Errorf("expected 1000 after truncation, got %d", len(logs))
	}
	if logs[len(logs)-1] != "new-entry" {
		t.Errorf("last entry should be new-entry, got %q", logs[len(logs)-1])
	}
}

// ---- calcInitialTableErrorWidth with target error ----

func TestCalcInitialTableErrorWidth_TargetError(t *testing.T) {
	tgt := stats.NewTargetStats("example.com")
	tgt.OnFailure("This is a very long error message that exceeds normal width expectations!!")
	got := calcInitialTableErrorWidth([]*stats.TargetStats{tgt}, "Error", 5)
	if got < len("This is a very long error message") {
		t.Errorf("expected width to include target error, got %d", got)
	}
}

// ---- truncateToDisplayWidth width <= 3 ----

func TestTruncateToDisplayWidth_VerySmall(t *testing.T) {
	got := truncateToDisplayWidth("hello", 2)
	if got != ".." {
		t.Errorf("width=2 long string: got %q, want %q", got, "..")
	}
	got = truncateToDisplayWidth("hello", 1)
	if got != "." {
		t.Errorf("width=1 long string: got %q, want %q", got, ".")
	}
}

// ---- formatCellText: width=0 and AlignLeft padding ----

func TestFormatCellText_ZeroWidth(t *testing.T) {
	got := formatCellText("x", 0, tview.AlignLeft)
	if got != "" {
		t.Errorf("width=0: got %q, want empty", got)
	}
}

func TestFormatCellText_AlignLeftPadding(t *testing.T) {
	got := formatCellText("hi", 5, tview.AlignLeft)
	if got != "hi   " {
		t.Errorf("AlignLeft pad: got %q, want %q", got, "hi   ")
	}
}

// ---- fitWidthsToAvailable: length mismatch and minWidth > maxWidth ----

func TestFitWidthsToAvailable_LengthMismatch(t *testing.T) {
	_, ok := fitWidthsToAvailable([]int{10, 20}, []int{5}, []int{50, 50}, 30)
	if ok {
		t.Error("expected false for length mismatch")
	}
}

func TestFitWidthsToAvailable_MinGTMax(t *testing.T) {
	// minWidths[0]=10 > maxWidths[0]=5 → maxWidths[0] clamped to 10
	widths, ok := fitWidthsToAvailable([]int{8}, []int{10}, []int{5}, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if widths[0] != 10 {
		t.Errorf("expected width=10 (clamped), got %d", widths[0])
	}
}

// ---- displaySourceIPForDst: IPv6-like string without sourceIPv6 ----

func TestDisplaySourceIPForDst_ColonNoSrc6(t *testing.T) {
	// String contains ":" but is not parseable as IP → falls through to colon check
	got := displaySourceIPForDst("not::valid::ip", "", "")
	if got != "Auto" {
		t.Errorf("expected Auto, got %q", got)
	}
	got = displaySourceIPForDst("not::valid::ip", "10.0.0.1", "")
	if got != "Auto" {
		t.Errorf("expected Auto for IPv6-like without src6, got %q", got)
	}
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

// ---- gridStepsForHeight: height=1 → totalSteps clamped to 1 ----

func TestGridStepsForHeight_One(t *testing.T) {
	totalSteps, _, _, _, _ := gridStepsForHeight(1)
	if totalSteps != 1 {
		t.Errorf("expected totalSteps=1 for height=1, got %d", totalSteps)
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

// ---- Run() with traceEnabled, portEnabled, both, initialLogs, doneCh, keyboard ----

func makeSimScreen(t *testing.T) (func() *tview.Application, chan tcell.SimulationScreen) {
	t.Helper()
	ch := make(chan tcell.SimulationScreen, 1)
	return func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		screen.Init()
		app.SetScreen(screen)
		screen.SetSize(200, 50)
		ch <- screen
		return app
	}, ch
}

func TestRunWithTraceEnabled(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56, TraceEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunWithPortEnabled(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{
		{Port: 443, Protocol: "tcp", Status: "Open"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunWithBothTracAndPort(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{
		{Port: 80, Protocol: "tcp", Status: "Closed"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunWithInitialLogs(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	logs := []string{"[red]initial error 1[-]", "[red]initial error 2[-]"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, InitialLogs: logs})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunWithDoneChClosed(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	doneCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, DoneCh: doneCh, PacketSize: 56})
	}()

	screen := <-screenCh
	// Close doneCh to trigger "Finished" footer path
	time.Sleep(15 * time.Millisecond)
	close(doneCh)
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunKeyboardStop(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	stopCalled := make(chan struct{}, 1)
	onStop := func() { stopCalled <- struct{}{} }

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, OnStop: onStop})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Press 's' to stop
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	time.Sleep(30 * time.Millisecond)
	// Press 'q' to quit
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunKeyboardReset(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.IncSent()
	target.OnSuccess(10*time.Millisecond, 64)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Press 'R' to reset stats
	screen.InjectKey(tcell.KeyRune, 'R', tcell.ModNone)
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunKeyboardRestart(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	restartCalled := make(chan struct{}, 1)
	onStop := func() {}
	onRestart := func() error {
		restartCalled <- struct{}{}
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, OnStop: onStop, OnRestart: onRestart})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone) // stop first
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'S', tcell.ModNone) // restart
	time.Sleep(50 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunKeyboardTab(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab cycles focus between table, graph, errorView
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- truncateToDisplayWidth: width <= 0 ----

func TestTruncateToDisplayWidth_ZeroWidth(t *testing.T) {
	if got := truncateToDisplayWidth("hello", 0); got != "" {
		t.Errorf("width=0: got %q, want empty", got)
	}
	if got := truncateToDisplayWidth("hello", -1); got != "" {
		t.Errorf("width=-1: got %q, want empty", got)
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

// ---- displaySourceIPForDst: IPv4 auto (no sourceIPv4) ----

func TestDisplaySourceIPForDst_IPv4Auto(t *testing.T) {
	// IPv4 destination but no sourceIPv4 → "Auto"
	got := displaySourceIPForDst("8.8.8.8", "", "2001:db8::1")
	if got != "Auto" {
		t.Errorf("expected Auto for IPv4 dst without src4, got %q", got)
	}
}

// ---- Run with Tab when trace + port enabled (covers more Tab branches) ----

func TestRunTabWithTraceAndPort(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{{Port: 443, Protocol: "tcp", Status: "Open"}}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab multiple times to cycle through: table→trace→port→graph→error→table
	for i := 0; i < 6; i++ {
		screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: 's' key when onStop is nil (stopRequested path without callback) ----

func TestRunKeyboardStopNoCallback(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")

	errCh := make(chan error, 1)
	go func() {
		// onStop=nil, onRestart=nil
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone) // stop (no callback)
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone) // second 's' is no-op (already stopped)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: target with loss → error log entry ----

func TestRunWithLossTarget(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")
	target.IncSent()
	target.OnFailure("Timeout")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(80 * time.Millisecond) // wait for at least one tick to update table
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- fitWidthsToAvailable: grow path (sum < available) ----

func TestFitWidthsToAvailable_GrowPath(t *testing.T) {
	// desired sum=9 < available=15 → grow loop expands columns
	widths, ok := fitWidthsToAvailable([]int{3, 3, 3}, []int{2, 2, 2}, []int{10, 10, 10}, 15)
	if !ok {
		t.Fatal("expected ok for grow path")
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	if total != 15 {
		t.Errorf("grow: total width should be 15, got %d", total)
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

// ---- Run: table key scroll (KeyDown when table is focused) ----

func TestRunTableScrollKeys(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	// Many targets so there are rows to scroll
	targets := make([]*stats.TargetStats, 12)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: targets, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Table should be focused initially; send scroll keys
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyUp, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgUp, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: onRestart returning error ----

func TestRunRestartError(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	onStop := func() {}
	restartErr := errors.New("restart failed")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, OnStop: onStop, OnRestart: func() error { return restartErr }})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'S', tcell.ModNone)
	time.Sleep(60 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
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

// ---- Run: Tab with traceEnabled only (covers traceView→graphView branch) ----

func TestRunTabTraceOnly(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab cycles: table→traceView→graphView→errorView→table
	for i := 0; i < 5; i++ {
		screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: Tab with portEnabled only (covers table→portView branch) ----

func TestRunTabPortOnly(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{{Port: 443, Protocol: "tcp", Status: "Open"}}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab cycles: table→portView→graphView→errorView→table
	for i := 0; i < 5; i++ {
		screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: narrow screen triggers compact layout path ----

func makeNarrowSimScreen(t *testing.T) (func() *tview.Application, chan tcell.SimulationScreen) {
	t.Helper()
	ch := make(chan tcell.SimulationScreen, 1)
	return func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		screen.Init()
		app.SetScreen(screen)
		// Width 70: too narrow for full 13-col layout (~89 min) but fits compact (~55 min)
		screen.SetSize(70, 30)
		ch <- screen
		return app
	}, ch
}

func TestRunNarrowScreenCompactLayout(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeNarrowSimScreen(t)
	newApplication = factory

	targets := make([]*stats.TargetStats, 3)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: targets, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	// Wait for multiple ticks so updateTable runs and compact layout is evaluated
	time.Sleep(150 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
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

// ---- displaySourceIPForDst: IPv6 dst with sourceIPv6 set ----

func TestDisplaySourceIPForDst_IPv6WithSrc6(t *testing.T) {
	got := displaySourceIPForDst("2001:db8::1", "", "2001:db8::cafe")
	if got != "2001:db8::cafe" {
		t.Errorf("IPv6 dst with src6: got %q, want %q", got, "2001:db8::cafe")
	}
}

// ---- truncateToDisplayWidth: zero-width character ----

func TestTruncateToDisplayWidth_ZeroWidthChar(t *testing.T) {
	// U+0300 COMBINING GRAVE ACCENT has runewidth=0 → code converts to 1
	// "\u0300hello" displayWidth=5 (0+1+1+1+1+1), truncate at width=4 → limit=1
	got := truncateToDisplayWidth("\u0300hello", 4)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("zero-width char truncation: got %q, expected ellipsis suffix", got)
	}
}

// ---- fitWidthsToAvailable: grow loop !changed (all at max, still below available) ----

func TestFitWidthsToAvailable_GrowNoChange(t *testing.T) {
	// desired=maxWidths=5, available=20, sum(5)=5 < 20
	// All widths already at max → grow loop fires !changed and breaks
	widths, ok := fitWidthsToAvailable([]int{5}, []int{1}, []int{5}, 20)
	if !ok {
		t.Fatal("expected ok")
	}
	// Result should be capped at maxWidths[0]=5 even though available=20
	if widths[0] != 5 {
		t.Errorf("expected width capped at maxWidth 5, got %d", widths[0])
	}
}

// ---- Run: portEnabled with no port results (rowCount==0 path) ----

func TestRunPortEnabledNoResults(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	// No PortResults → triggers "Waiting for results..." row

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(120 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: high RTT and jitter triggers alert state ----

func TestRunWithHighRTTAndJitter(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")
	// Simulate high RTT (> 200ms red threshold) and high jitter (> 50ms red threshold)
	for i := 0; i < 5; i++ {
		target.IncSent()
		target.OnSuccess(300*time.Millisecond, 64)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	// Wait > 100ms for ticker to fire so updateTable runs and alerts are checked
	time.Sleep(150 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: high loss rate triggers lossRed alert ----

func TestRunWithHighLossAlert(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")
	// Simulate >80% loss: 9 packets lost, 1 success
	for i := 0; i < 9; i++ {
		target.IncSent()
		target.OnFailure("Timeout")
	}
	target.IncSent()
	target.OnSuccess(10*time.Millisecond, 64)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: traceEnabled with ticker (>100ms wait) to cover updateTable trace path ----

func TestRunWithTraceAndTicker(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "10.0.0.2", "1.1.1.1"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56, TraceEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond) // wait for ticker to fire and updateTable to run
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: portEnabled with ticker (>100ms wait) to cover updateTable port path ----

func TestRunWithPortAndTicker(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{
		{Port: 443, Protocol: "tcp", Status: "Open"},
		{Port: 80, Protocol: "tcp", Status: "Closed"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: both trace and port with ticker ----

func TestRunWithBothAndTicker(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})
	target.PortResults = []*stats.PortCheckResult{
		{Port: 443, Protocol: "tcp", Status: "Open"},
	}

	// Two targets so route separator row fires
	target2 := stats.NewTargetStats("example2.com")
	target2.SetIP("8.8.8.8")
	target2.SetTraceHops([]string{"10.0.0.1"})
	target2.PortResults = []*stats.PortCheckResult{
		{Port: 80, Protocol: "tcp", Status: "Filtered"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target, target2}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", SourceIPv6: "2001::1", PacketSize: 56, TraceEnabled: true, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- displaySourceIPForDst: covers % zone ID path and remaining branches ----

func TestDisplaySourceIPForDst_ZoneID(t *testing.T) {
	// IPv6 with zone ID (e.g. "fe80::1%eth0") → strip % and treat as IPv6
	got := displaySourceIPForDst("fe80::1%eth0", "", "2001::cafe")
	if got != "2001::cafe" {
		t.Errorf("zone ID: got %q, want %q", got, "2001::cafe")
	}
}

func TestDisplaySourceIPForDst_HostnameWithSrc4(t *testing.T) {
	// Non-IP hostname with no colon → falls to final IPv4 check
	got := displaySourceIPForDst("hostname.local", "10.0.0.1", "")
	if got != "10.0.0.1" {
		t.Errorf("hostname with src4: got %q, want %q", got, "10.0.0.1")
	}
}

func TestDisplaySourceIPForDst_ColonWithSrc6(t *testing.T) {
	// Non-IP with colon and sourceIPv6 set → return sourceIPv6
	got := displaySourceIPForDst("not::valid::ip", "10.0.0.1", "2001::cafe")
	if got != "2001::cafe" {
		t.Errorf("colon with src6: got %q, want %q", got, "2001::cafe")
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

// ---- Run: scroll key with 1 target (maxOffset < 0 path) ----

func TestRunScrollKeyWithFewTargets(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	// Only 1 target → rowCount=2 < tableMaxRows+1=11 → maxOffset < 0 → clamped to 0
	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Press scroll keys while table is focused (few targets → maxOffset < 0 path)
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyUp, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgUp, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: target with IP==host covers calcColumnWidths value=view.IP branch ----

func TestRunWithIPAsHost(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	// Host and IP are the same → value = view.IP branch in calcColumnWidths
	target := stats.NewTargetStats("1.2.3.4")
	target.SetIP("1.2.3.4")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond) // wait for ticker
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: 'R' key with traceEnabled clears TraceHops and calls onResetTrace ----

func TestRunResetTraceKey(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})

	resetCalled := make(chan struct{}, 1)
	onResetTrace := func() { resetCalled <- struct{}{} }

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true, OnResetTrace: onResetTrace})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'R', tcell.ModNone)

	// onResetTrace must be called.
	select {
	case <-resetCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("onResetTrace was not called after 'R' key")
	}

	// TraceHops must have been cleared.
	if hops := target.GetView().TraceHops; len(hops) != 0 {
		t.Errorf("TraceHops not cleared after 'R': got %v", hops)
	}

	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: 'R' key with portEnabled calls onResetPort ----

func TestRunResetPortKey(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	result := &stats.PortCheckResult{Port: 80, Protocol: "tcp"}
	result.SetResult("Open", 5*time.Millisecond)
	target.PortResults = []*stats.PortCheckResult{result}

	resetCalled := make(chan struct{}, 1)
	onResetPort := func() { resetCalled <- struct{}{} }

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true, OnResetPort: onResetPort})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'R', tcell.ModNone)

	select {
	case <-resetCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("onResetPort was not called after 'R' key")
	}

	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: port status change logs message to error pane ----

func TestRunPortStatusChangeLogged(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	result := &stats.PortCheckResult{Port: 443, Protocol: "tcp"}
	result.SetResult("Open", 5*time.Millisecond)
	target.PortResults = []*stats.PortCheckResult{result}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	// Let one render cycle run so the initial status is recorded
	time.Sleep(120 * time.Millisecond)

	// Change port status to trigger change detection on next render
	result.SetResult("Closed", 0)
	time.Sleep(120 * time.Millisecond)

	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: port with non-zero LastChange covers changeStr=formatLossAgo path ----

func TestRunWithPortLastChange(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	result := &stats.PortCheckResult{Port: 443, Protocol: "tcp", Status: "Checking..."}
	target.PortResults = []*stats.PortCheckResult{result}
	// Trigger LastChange by changing status
	result.SetResult("Open", 10*time.Millisecond)
	result.SetResult("Closed", 5*time.Millisecond) // status change sets LastChange

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Tests for refactored helper functions ----

func TestPaddedCell(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		colW  int
		check func(string) bool
	}{
		{
			name: "leading space",
			text: "abc",
			colW: 10,
			check: func(s string) bool {
				return len(s) > 0 && s[0] == ' '
			},
		},
		{
			name: "padded to width",
			text: "abc",
			colW: 10,
			check: func(s string) bool {
				return len(s) == 10
			},
		},
		{
			name: "empty text",
			text: "",
			colW: 5,
			check: func(s string) bool {
				return len(s) == 5
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paddedCell(tt.text, tt.colW)
			if !tt.check(got) {
				t.Fatalf("paddedCell(%q, %d) = %q, check failed", tt.text, tt.colW, got)
			}
		})
	}
}

func TestStatusColorTag(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"Open", "[green]"},
		{"Closed", "[red]"},
		{"Filtered", "[yellow]"},
		{"Open|Filtered", "[yellow]"},
		{"Unknown", "[white]"},
		{"", "[white]"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := statusColorTag(tt.status); got != tt.want {
				t.Fatalf("statusColorTag(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestRenderTracerouteTable(t *testing.T) {
	t.Run("no data targets", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		got := renderTracerouteTable([]*stats.TargetStats{target}, 80)
		if !strings.Contains(got, "Host") || !strings.Contains(got, "Route") {
			t.Fatalf("expected table headers, got: %s", got)
		}
	})

	t.Run("with data targets full mode", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetTraceHops([]string{"1.1.1.1", "2.2.2.2", "8.8.8.8"})
		got := renderTracerouteTable([]*stats.TargetStats{target}, 120)
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname in output, got: %s", got)
		}
		if !strings.Contains(got, "Hops") {
			t.Fatalf("expected full mode with Hops column, got: %s", got)
		}
	})

	t.Run("compact mode narrow width", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetTraceHops([]string{"1.1.1.1"})
		got := renderTracerouteTable([]*stats.TargetStats{target}, 30)
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname in output, got: %s", got)
		}
		if strings.Contains(got, "Hops") {
			t.Fatalf("expected compact mode without Hops column, got: %s", got)
		}
	})

	t.Run("multiple targets", func(t *testing.T) {
		t1 := stats.NewTargetStats("a.com")
		t1.SetTraceHops([]string{"1.1.1.1"})
		t2 := stats.NewTargetStats("b.com")
		t2.SetTraceHops([]string{"2.2.2.2"})
		got := renderTracerouteTable([]*stats.TargetStats{t1, t2}, 120)
		if !strings.Contains(got, "a.com") || !strings.Contains(got, "b.com") {
			t.Fatalf("expected both hosts in output, got: %s", got)
		}
	})
}

func TestRenderPortMonitorTable(t *testing.T) {
	t.Run("no data targets", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		lastStatuses := make(map[string]string)
		errorLogs := []string{}
		errorView := tview.NewTextView()
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, &errorLogs, errorView)
		if !strings.Contains(got, "Waiting for results") {
			t.Fatalf("expected waiting message, got: %s", got)
		}
	})

	t.Run("with data full mode", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetIP("1.1.1.1")
		result := &stats.PortCheckResult{Port: 443, Protocol: "tcp"}
		result.SetResult("Open", 5*time.Millisecond)
		target.PortResults = []*stats.PortCheckResult{result}

		lastStatuses := make(map[string]string)
		errorLogs := []string{}
		errorView := tview.NewTextView()
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, &errorLogs, errorView)
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname, got: %s", got)
		}
		if !strings.Contains(got, "Service") {
			t.Fatalf("expected full mode with Service column, got: %s", got)
		}
	})

	t.Run("compact mode narrow width", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetIP("1.1.1.1")
		result := &stats.PortCheckResult{Port: 80, Protocol: "tcp"}
		result.SetResult("Closed", 0)
		target.PortResults = []*stats.PortCheckResult{result}

		lastStatuses := make(map[string]string)
		errorLogs := []string{}
		errorView := tview.NewTextView()
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 30, lastStatuses, &errorLogs, errorView)
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname, got: %s", got)
		}
		if strings.Contains(got, "Service") {
			t.Fatalf("expected compact mode without Service column, got: %s", got)
		}
	})

	t.Run("status change logs", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetIP("1.1.1.1")
		result := &stats.PortCheckResult{Port: 443, Protocol: "tcp"}
		result.SetResult("Open", 5*time.Millisecond)
		target.PortResults = []*stats.PortCheckResult{result}

		lastStatuses := map[string]string{
			"example.com|443/tcp": "Closed",
		}
		errorLogs := []string{}
		errorView := tview.NewTextView()
		renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, &errorLogs, errorView)

		if len(errorLogs) == 0 {
			t.Fatal("expected status change log entry")
		}
		if !strings.Contains(errorLogs[0], "Open") {
			t.Fatalf("expected log to mention Open, got: %s", errorLogs[0])
		}
	})
}

func TestMakeDoubleBorderDrawFunc(t *testing.T) {
	borderColor := tcell.ColorWhite
	drawFunc := makeDoubleBorderDrawFunc(" Test Title ", &borderColor)

	if drawFunc == nil {
		t.Fatal("expected non-nil drawFunc")
	}

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()

	// Should not panic with small dimensions
	ix, iy, iw, ih := drawFunc(screen, 0, 0, 1, 1)
	if iw != -1 || ih != -1 {
		t.Fatalf("small dims: inner w=%d h=%d, want -1 -1", iw, ih)
	}
	_ = ix
	_ = iy

	// Test with normal dimensions
	ix, iy, iw, ih = drawFunc(screen, 0, 0, 40, 10)
	if ix != 1 || iy != 1 || iw != 38 || ih != 8 {
		t.Fatalf("normal dims: ix=%d iy=%d iw=%d ih=%d, want 1 1 38 8", ix, iy, iw, ih)
	}

	// Verify border color can be changed dynamically
	borderColor = tcell.ColorRed
	drawFunc(screen, 0, 0, 40, 10)
}

func newSimApp(t *testing.T) (*tview.Application, tcell.SimulationScreen) {
	t.Helper()
	app := tview.NewApplication()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	screen.SetSize(120, 40)
	app.SetScreen(screen)
	return app, screen
}

func TestRunOnAddHost_CallbackInvoked(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	addCh := make(chan string, 1)

	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			// Simulate 'a' key to open add dialog
			screen.InjectKey(tcell.KeyRune, 'a', tcell.ModNone)
			time.Sleep(20 * time.Millisecond)
			// Type hostname (use IP to avoid triggering key shortcuts)
			for _, r := range "1.2.3.4" {
				screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			time.Sleep(20 * time.Millisecond)
			// Press Enter to confirm
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			time.Sleep(100 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("example.com")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnAddHost: func(host string) error {
			addCh <- host
			return errors.New("test stop") // return error so TUI doesn't reload
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var got string
	select {
	case got = <-addCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("OnAddHost was not called within timeout")
	}
	if got != "1.2.3.4" {
		t.Fatalf("OnAddHost called with %q, want %q", got, "1.2.3.4")
	}
}

func TestRunOnAddHost_EmptyHostIgnored(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	addCalled := false
	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			screen.InjectKey(tcell.KeyRune, 'a', tcell.ModNone)
			time.Sleep(10 * time.Millisecond)
			// Press Enter without typing anything
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			time.Sleep(30 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("example.com")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnAddHost: func(host string) error {
			addCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if addCalled {
		t.Fatal("OnAddHost should not be called for empty host")
	}
}

func TestRunOnDeleteHost_CallbackInvoked(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	deleteCh := make(chan string, 1)

	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			// Simulate 'd' key to open delete dialog
			screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
			time.Sleep(20 * time.Millisecond)
			// Type the hostname to delete
			for _, r := range "8.8.8.8" {
				screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			time.Sleep(20 * time.Millisecond)
			// Press Enter to confirm
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			time.Sleep(100 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("8.8.8.8")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnDeleteHost: func(host string) error {
			deleteCh <- host
			return errors.New("test stop")
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var got string
	select {
	case got = <-deleteCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("OnDeleteHost was not called within timeout")
	}
	if got != "8.8.8.8" {
		t.Fatalf("OnDeleteHost called with %q, want %q", got, "8.8.8.8")
	}
}

func TestRunOnDeleteHost_EscapeAborts(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	deleteCalled := false
	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
			time.Sleep(20 * time.Millisecond)
			// Type something then press Escape to cancel
			for _, r := range "8.8.8.8" {
				screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			time.Sleep(10 * time.Millisecond)
			screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
			time.Sleep(50 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("8.8.8.8")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnDeleteHost: func(host string) error {
			deleteCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if deleteCalled {
		t.Fatal("OnDeleteHost should not be called after Escape")
	}
}
