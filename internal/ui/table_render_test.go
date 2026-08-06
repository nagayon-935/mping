package ui

import (
	"fmt"
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
	priority := []int{0, 1, 2}

	widths, ok := fitWidthsToAvailable(desired, min, max, priority, priority, 30)
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

	_, ok = fitWidthsToAvailable(desired, min, max, priority, priority, 10)
	if ok {
		t.Fatalf("fitWidthsToAvailable should fail when available < sum(min)")
	}

	widths, ok = fitWidthsToAvailable(desired, min, max, priority, priority, 80)
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

// newTestTableRenderer builds a tableRenderer wired up the same way Run()
// does, sized directly via SetRect so update() can be driven without a full
// Application/Screen (mirroring the pattern already used for GraphView in
// graph_view_test.go).
func newTestTableRenderer(targets []*stats.TargetStats, width int) *tableRenderer {
	table := tview.NewTable().SetBorders(true).SetSelectable(false, false).SetFixed(1, 1)
	tablePane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(table, 0, 1, true)
	tablePane.SetBorder(true).SetTitle(" Ping Monitor ").SetBorderColor(tcell.ColorWhite)
	tablePane.SetRect(0, 0, width, 30)

	errorView := tview.NewTextView()
	vs := newViewState(errorView)

	return newTableRenderer(targets, "10.0.0.1", "", 56, false, false, false, nil, table, tablePane, nil, vs)
}

// TestTableRendererUpdate_SkipsCompactWhenFullLayoutFits guards the P3 fix:
// when the full-width layout fits (width=200, matching makeSimScreen's
// proven-wide value in run_refresh_test.go), update() must resolve to the
// full (non-compact) layout — buildCompactLayout's per-target GetView() work
// is now only computed as a fallback, so this pins the "skip" branch's
// observable outcome (compactLayout stays false, rowCount == len(targets)+1)
// even though the skip itself isn't directly instrumented.
func TestTableRendererUpdate_SkipsCompactWhenFullLayoutFits(t *testing.T) {
	targets := make([]*stats.TargetStats, 3)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
		targets[i].SetIP("10.0.0.1")
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}
	tr := newTestTableRenderer(targets, 200)
	tr.update()

	if tr.compactLayout {
		t.Fatal("expected full layout when terminal is wide, got compactLayout=true")
	}
	if tr.rowCount != len(targets)+1 {
		t.Errorf("rowCount: got %d, want %d", tr.rowCount, len(targets)+1)
	}
}

// TestTableRendererUpdate_FallsBackToCompactWhenNarrow is the companion
// case: when the full layout doesn't fit (width=70, matching
// makeNarrowSimScreen's proven-narrow value), update() must still fall back
// to the compact layout exactly as before the P3 change.
func TestTableRendererUpdate_FallsBackToCompactWhenNarrow(t *testing.T) {
	targets := make([]*stats.TargetStats, 3)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
		targets[i].SetIP("10.0.0.1")
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}
	tr := newTestTableRenderer(targets, 70)
	tr.update()

	if !tr.compactLayout {
		t.Fatal("expected compact layout fallback when terminal is narrow, got compactLayout=false")
	}
}

// TestTableRendererUpdate_VirtualizesOffscreenRows guards the P1 fix: only
// rows within the visible scroll window (plus rowRenderMargin on each side)
// should actually get SetCell'd; targets far outside that window must be
// left empty despite rowCount still reflecting every target.
func TestTableRendererUpdate_VirtualizesOffscreenRows(t *testing.T) {
	const numTargets = 100
	targets := make([]*stats.TargetStats, numTargets)
	for i := range targets {
		targets[i] = stats.NewTargetStats(fmt.Sprintf("host%03d", i))
		targets[i].SetIP("10.0.0.1")
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}
	tr := newTestTableRenderer(targets, 200)

	const midOffset = 50
	tr.table.SetOffset(midOffset, 0)
	tr.update()

	if tr.rowCount != numTargets+1 {
		t.Fatalf("rowCount: got %d, want %d (logical total must be unaffected by windowing)", tr.rowCount, numTargets+1)
	}

	// A target far before the window (e.g. row 1, target index 0) must be
	// empty: outside [midOffset-margin, midOffset+tableMaxRows+1+margin).
	if got := tr.table.GetCell(1, 0).Text; got != "" {
		t.Errorf("row 1 (far above window): expected empty cell, got %q", got)
	}
	// A target far after the window (last target) must also be empty.
	lastRow := numTargets
	if got := tr.table.GetCell(lastRow, 0).Text; got != "" {
		t.Errorf("row %d (far below window): expected empty cell, got %q", lastRow, got)
	}
	// A target inside the scrolled-to window must be populated.
	inWindowRow := midOffset + 1 // table row for target index == midOffset
	if got := tr.table.GetCell(inWindowRow, 0).Text; got == "" {
		t.Errorf("row %d (inside window): expected populated cell, got empty", inWindowRow)
	}
	// The header row (0) must always render regardless of scroll position.
	if got := tr.table.GetCell(0, 0).Text; got == "" {
		t.Error("header row: expected populated cell, got empty")
	}
}

// BenchmarkTableRendererUpdate_500Targets_Scrolled is the P1 fix's
// windowed-rendering proof. Pass 1 (GetView, alert/log tracking) still
// necessarily scans every target regardless of scroll position — that
// part of the per-tick cost scales with total target count no matter what,
// and correctly so, since an off-screen host's loss/alert state must still
// be tracked. What P1 caps is Pass 2's cell-rendering work: regardless of
// how far numTargets exceeds tableMaxRows, only the visible-plus-margin
// window's rows are ever SetCell'd here (~16-21 rows), rather than all
// 500. Compare this benchmark's allocs/op against a version of update()
// without the windowing (e.g. checking out the prior commit) to see that
// difference directly — this run alone mainly confirms Pass 2 doesn't
// panic or misbehave at a large scrolled offset.
func BenchmarkTableRendererUpdate_500Targets_Scrolled(b *testing.B) {
	const numTargets = 500
	targets := make([]*stats.TargetStats, numTargets)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
		targets[i].SetIP("10.0.0.1")
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}
	tr := newTestTableRenderer(targets, 200)
	tr.table.SetOffset(250, 0)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tr.update()
	}
}

// BenchmarkTableRendererUpdate_100Targets is the P2 fix's alloc-reduction
// proof: run with `go test -bench=. -benchmem` and compare against the
// pre-P2 commit — GetView() previously ran once per target for width calc,
// once per target in Pass 1, and (before the P3 fix) again inside
// buildCompactLayout, each copying up to historySize RTT history entries.
// After P2, each target's GetView() runs exactly once per update() call.
func BenchmarkTableRendererUpdate_100Targets(b *testing.B) {
	const numTargets = 100
	targets := make([]*stats.TargetStats, numTargets)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
		targets[i].SetIP("10.0.0.1")
		for j := 0; j < 500; j++ {
			targets[i].IncSent()
			targets[i].OnSuccess(time.Duration(j)*time.Millisecond, 64)
		}
	}
	tr := newTestTableRenderer(targets, 200)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tr.update()
	}
}

func TestBuildCompactLayout(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.OnSuccess(12*time.Millisecond, 64)
	target.SetIfaceMTU(1500)
	layout := buildCompactLayout([]stats.TargetView{target.GetView()}, 56, "10.0.0.2", "", 20)
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

// TestMainTableColumns_DynamicFlags is TestDynamicColumnIndices's
// column-struct-model successor (TD-21②): it checks which columns are
// marked dynamic (width tracks rendered content) rather than returning a
// separate index list that could drift out of sync with the column slice.
func TestMainTableColumns_DynamicFlags(t *testing.T) {
	tests := []struct {
		name       string
		dnsEnabled bool
		asnEnabled bool
		ptrEnabled bool
		want       []string // names of columns expected to be dynamic, in order
	}{
		{"neither enabled", false, false, false, []string{"Src IP", "Dst IP"}},
		{"dns only", true, false, false, []string{"Src IP", "Dst IP", "DNS"}},
		{"asn only", false, true, false, []string{"Src IP", "Dst IP", "ASN"}},
		{"ptr only", false, false, true, []string{"Src IP", "Dst IP", "PTR"}},
		{"all enabled", true, true, true, []string{"Src IP", "Dst IP", "DNS", "ASN", "PTR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols := mainTableColumns(tt.dnsEnabled, tt.asnEnabled, tt.ptrEnabled, false)
			var got []string
			for _, c := range cols {
				if c.dynamic {
					got = append(got, c.name)
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("dynamic columns = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("dynamic columns = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestMainTableColumns_DSCP verifies the DSCP column follows ASN/PTR's
// dynamic-column pattern (absent by default, present when enabled) but is
// itself NOT a "dynamic" (width-tracks-content) column — it mirrors TTL's
// fixed-width short-text convention instead, since its values are short
// codepoint names/numbers, not variable-length text like a hostname or PTR
// record.
func TestMainTableColumns_DSCP(t *testing.T) {
	withoutDSCP := mainTableColumns(false, false, false, false)
	if idx := columnsByName(withoutDSCP, "DSCP"); idx != -1 {
		t.Fatalf("DSCP column present when dscpEnabled=false: index %d", idx)
	}

	withDSCP := mainTableColumns(false, false, false, true)
	idx := columnsByName(withDSCP, "DSCP")
	if idx == -1 {
		t.Fatal("DSCP column missing when dscpEnabled=true")
	}
	if withDSCP[idx].dynamic {
		t.Error("DSCP column should not be dynamic (fixed-width, like TTL)")
	}
}

func TestDSCPColumnRender(t *testing.T) {
	cols := mainTableColumns(false, false, false, true)
	idx := columnsByName(cols, "DSCP")
	if idx == -1 {
		t.Fatal("DSCP column missing")
	}

	tests := []struct {
		name     string
		lastDSCP int
		want     string
	}{
		{"no observation yet", 0, "-"},
		{"EF observed", 46 << 2, "EF"},
		{"CS0 observed", 0, "-"}, // ambiguous with "unobserved" by design; see dscpDisplayString
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := columnRowContext{view: stats.TargetView{LastDSCP: tt.lastDSCP}}
			if got := cols[idx].render(ctx); got != tt.want {
				t.Errorf("render(LastDSCP=%d) = %q, want %q", tt.lastDSCP, got, tt.want)
			}
		})
	}
}

// TestRenderRowTexts is TestBuildFullColumns's column-struct-model successor
// (TD-21③): renderRowTexts + mainTableColumns replace buildFullColumns.
func TestRenderRowTexts(t *testing.T) {
	view := stats.TargetView{
		Host:      "example.com",
		IP:        "1.1.1.1",
		ASN:       "AS15169",
		PTR:       "one.one.one.one",
		DNSServer: "8.8.8.8",
		Recv:      10,
		Loss:      2,
		LastRTT:   12 * time.Millisecond,
		AvgRTT:    10 * time.Millisecond,
		Jitter:    2 * time.Millisecond,
		IfaceMTU:  1500,
		LastTTL:   64,
	}
	ctx := columnRowContext{view: view, sourceIPv4: "10.0.0.2", packetSize: 56, lossRate: calcLossRate(view)}

	// Case 1: dnsEnabled = false, asnEnabled = true, ptrEnabled = false
	cols := mainTableColumns(false, true, false, false)
	texts := renderRowTexts(cols, ctx)
	if texts[0] != "10.0.0.2" {
		t.Fatalf("src: got %q", texts[0])
	}
	if ctx.lossRate <= 0 {
		t.Fatalf("loss rate: got %v", ctx.lossRate)
	}
	if len(texts) != 14 {
		t.Fatalf("texts len: got %d", len(texts))
	}
	// Dst IP should include hostname when host != IP
	if texts[1] != "example.com (1.1.1.1)" {
		t.Fatalf("dst ip: got %q", texts[1])
	}
	if texts[2] != "AS15169" {
		t.Fatalf("asn: got %q", texts[2])
	}

	// Case 2: dnsEnabled = true, asnEnabled = true, ptrEnabled = false
	cols2 := mainTableColumns(true, true, false, false)
	texts2 := renderRowTexts(cols2, ctx)
	if len(texts2) != 15 {
		t.Fatalf("texts len (dns enabled): got %d", len(texts2))
	}
	if texts2[1] != "example.com (1.1.1.1)" {
		t.Fatalf("dst ip: got %q", texts2[1])
	}
	if texts2[2] != "8.8.8.8" {
		t.Fatalf("dns: got %q, expected 8.8.8.8", texts2[2])
	}
	if texts2[3] != "AS15169" {
		t.Fatalf("asn: got %q", texts2[3])
	}

	// Case 3: dnsEnabled = true, asnEnabled = true, ptrEnabled = true
	cols3 := mainTableColumns(true, true, true, false)
	texts3 := renderRowTexts(cols3, ctx)
	if len(texts3) != 16 {
		t.Fatalf("texts len (ptr enabled): got %d", len(texts3))
	}
	if texts3[4] != "one.one.one.one" {
		t.Fatalf("ptr: got %q, expected one.one.one.one", texts3[4])
	}

	// PTR column falls back to blank (not a placeholder) when the target's
	// PTR field is unset, e.g. lookup failed or hasn't completed yet — the
	// plain IP is still visible via the always-present Dst IP column.
	viewNoPTR := view
	viewNoPTR.PTR = ""
	ctxNoPTR := columnRowContext{view: viewNoPTR, sourceIPv4: "10.0.0.2", packetSize: 56, lossRate: calcLossRate(viewNoPTR)}
	textsNoPTR := renderRowTexts(cols3, ctxNoPTR)
	if textsNoPTR[4] != "" {
		t.Fatalf("ptr fallback: got %q, want empty", textsNoPTR[4])
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

// TestRenderRowCells is TestBuildFullRowCells's column-struct-model
// successor (TD-21③): renderRowCells replaces buildFullRowCells and its
// hardcoded "column index + dns/asn offset" color switch — each column now
// owns its own color logic via column.color.
func TestRenderRowCells(t *testing.T) {
	view := stats.TargetView{
		Host: "d", IP: "d", ASN: "AS123",
		Recv: 1, Loss: 2,
		LastRTT: 300 * time.Millisecond, AvgRTT: 300 * time.Millisecond, Jitter: 60 * time.Millisecond,
		IfaceMTU: 1500, LastTTL: 64,
		LastError: "err",
	}
	ctx := columnRowContext{view: view, packetSize: 56, lossRate: 90.0}

	// Case 1: dnsEnabled = false, asnEnabled = true, ptrEnabled = false
	cols := mainTableColumns(false, true, false, false)
	widths := make([]int, len(cols))
	for i := range widths {
		widths[i] = 5
	}
	aligns := make([]int, len(cols))
	texts := renderRowTexts(cols, ctx)
	cells := renderRowCells(cols, texts, widths, aligns, ctx, tcell.ColorWhite)
	if len(cells) != len(cols) {
		t.Fatalf("cells len mismatch")
	}
	errorIdx := columnsByName(cols, "Error")
	if cells[errorIdx].Color == tcell.ColorWhite {
		t.Fatalf("expected error cell colored")
	}

	// Case 2: dnsEnabled = true, asnEnabled = true, ptrEnabled = false
	cols2 := mainTableColumns(true, true, false, false)
	widths2 := make([]int, len(cols2))
	for i := range widths2 {
		widths2[i] = 5
	}
	aligns2 := make([]int, len(cols2))
	texts2 := renderRowTexts(cols2, ctx)
	cells2 := renderRowCells(cols2, texts2, widths2, aligns2, ctx, tcell.ColorWhite)
	if len(cells2) != len(cols2) {
		t.Fatalf("cells len mismatch (dns enabled)")
	}

	// Case 3: dnsEnabled = true, asnEnabled = true, ptrEnabled = true
	cols3 := mainTableColumns(true, true, true, false)
	widths3 := make([]int, len(cols3))
	for i := range widths3 {
		widths3[i] = 5
	}
	aligns3 := make([]int, len(cols3))
	texts3 := renderRowTexts(cols3, ctx)
	cells3 := renderRowCells(cols3, texts3, widths3, aligns3, ctx, tcell.ColorWhite)
	if len(cells3) != len(cols3) {
		t.Fatalf("cells len mismatch (ptr enabled)")
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

func TestFormatRTT(t *testing.T) {
	if got := formatRTT(0); got != "-" {
		t.Fatalf("expected '-', got %q", got)
	}
	if got := formatRTT(10 * time.Millisecond); !strings.Contains(got, "ms") {
		t.Fatalf("expected ms, got %q", got)
	}
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
	_, ok := fitWidthsToAvailable([]int{10, 20}, []int{5}, []int{50, 50}, []int{0, 1}, []int{0, 1}, 30)
	if ok {
		t.Error("expected false for length mismatch")
	}
}

func TestFitWidthsToAvailable_MinGTMax(t *testing.T) {
	// minWidths[0]=10 > maxWidths[0]=5 → maxWidths[0] clamped to 10
	widths, ok := fitWidthsToAvailable([]int{8}, []int{10}, []int{5}, []int{0}, []int{0}, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if widths[0] != 10 {
		t.Errorf("expected width=10 (clamped), got %d", widths[0])
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

// ---- fitWidthsToAvailable: grow path (sum < available) ----

func TestFitWidthsToAvailable_GrowPath(t *testing.T) {
	// desired sum=9 < available=15 → grow loop expands columns
	widths, ok := fitWidthsToAvailable([]int{3, 3, 3}, []int{2, 2, 2}, []int{10, 10, 10}, []int{0, 1, 2}, []int{0, 1, 2}, 15)
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
	widths, ok := fitWidthsToAvailable([]int{5}, []int{1}, []int{5}, []int{0}, []int{0}, 20)
	if !ok {
		t.Fatal("expected ok")
	}
	// Result should be capped at maxWidths[0]=5 even though available=20
	if widths[0] != 5 {
		t.Errorf("expected width capped at maxWidth 5, got %d", widths[0])
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
