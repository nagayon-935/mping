package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// newPlanRenderer builds a table renderer for targets under opts, with the
// rendered texts planColumns works from.
func newPlanRenderer(t *testing.T, targets []*stats.TargetStats, opts columnOptions) (*tableRenderer, [][]string) {
	t.Helper()
	table := tview.NewTable().SetBorders(true)
	pane := tview.NewFlex().AddItem(table, 0, 1, true)
	tr := newTableRenderer(targets, sourceIPs{v4: "10.0.0.5"}, 56, opts, nil, table, pane, nil, newViewState(tview.NewTextView()))
	_, texts := tr.renderTexts(fetchViews(targets, tr.sparkPoints))
	return tr, texts
}

func replyingTarget(ip string, rtt time.Duration) *stats.TargetStats {
	target := stats.NewTargetStats(ip)
	target.SetIP(ip)
	target.IncSent()
	target.OnSuccess(rtt, 64)
	return target
}

func shownNames(tr *tableRenderer, plan columnPlan) []string {
	names := make([]string, len(plan.active))
	for j, i := range plan.active {
		names[j] = tr.cols[i].name
	}
	return names
}

func TestPlanColumns_HidesSupplementaryBeforeRequestedColumns(t *testing.T) {
	tests := []struct {
		name       string
		opts       columnOptions
		wantHidden []string
	}{
		{
			name:       "no options: supplementary columns only",
			opts:       columnOptions{},
			wantHidden: []string{"SIZE", "MTU", "SRC", "LAST LOSS", "LOST", "RCV", "TTL", "AVG"},
		},
		{
			name: "requested columns outlive every supplementary one",
			opts: columnOptions{ASN: true, PTR: true, MTU: true, SizeExplicit: true, SourceExplicit: true},
			wantHidden: []string{
				"LAST LOSS", "LOST", "RCV", "TTL", "AVG",
				"SIZE", "SRC", "ASN", "PTR", "PMTU",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", 12*time.Millisecond)}, tt.opts)

			// Act: the narrowest table that still fits, where only the core
			// columns can stay.
			var plan columnPlan
			for width := 20; width <= 200 && !plan.ok; width++ {
				plan = tr.planColumns(texts, width)
			}

			// Assert
			if !plan.ok {
				t.Fatal("core columns did not fit")
			}
			if !slices.Equal(plan.hidden, tt.wantHidden) {
				t.Errorf("hidden = %v, want %v", plan.hidden, tt.wantHidden)
			}
			if got, want := shownNames(tr, plan), []string{"DST", "LOSS%", "RTT", "JIT"}; !slices.Equal(got, want) {
				t.Errorf("shown = %v, want %v", got, want)
			}
		})
	}
}

func TestPlanColumns_HidesNothingWhenEverythingFits(t *testing.T) {
	// Arrange
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", 12*time.Millisecond)}, columnOptions{})

	// Act
	plan := tr.planColumns(texts, 200)

	// Assert
	if !plan.ok || len(plan.hidden) != 0 {
		t.Fatalf("plan = %+v, want every column shown", plan)
	}
}

func TestPlanColumns_NeverCutsNumbers(t *testing.T) {
	// Arrange: a long RTT value in a terminal that must hide columns.
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", 679842*time.Microsecond)}, columnOptions{})

	for _, width := range []int{120, 90, 70, 50} {
		// Act
		plan := tr.planColumns(texts, width)

		// Assert
		if !plan.ok {
			t.Fatalf("width %d: core columns did not fit", width)
		}
		for j, i := range plan.active {
			if tr.cols[i].align != tview.AlignRight || tr.cols[i].dynamic {
				continue
			}
			if text := texts[0][i]; plan.widths[j] < len(text) {
				t.Errorf("width %d: %s is %d wide, value %q cut", width, tr.cols[i].name, plan.widths[j], text)
			}
			if name := tr.cols[i].name; plan.widths[j] < len(name) {
				t.Errorf("width %d: heading %s cut to %d", width, name, plan.widths[j])
			}
		}
	}
}

func TestPlanColumns_ErrorColumnOnlyWithErrors(t *testing.T) {
	tests := []struct {
		name    string
		failing bool
		wantERR bool
	}{
		{"no errors: no ERR column", false, false},
		{"an error: ERR column shown", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			target := replyingTarget("10.0.0.1", 12*time.Millisecond)
			if tt.failing {
				target.IncSent()
				target.OnFailure("Timeout")
			}
			tr, texts := newPlanRenderer(t, []*stats.TargetStats{target}, columnOptions{})

			// Act
			plan := tr.planColumns(texts, 200)

			// Assert
			if got := slices.Contains(shownNames(tr, plan), "ERR"); got != tt.wantERR {
				t.Errorf("ERR shown = %v, want %v (texts %q)", got, tt.wantERR, texts[0])
			}
		})
	}
}

func TestPaneTitle_NamesHiddenColumns(t *testing.T) {
	// Arrange
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{
		replyingTarget("10.0.0.1", 12*time.Millisecond),
		replyingTarget("10.0.0.2", 15*time.Millisecond),
	}, columnOptions{SourceExplicit: true})
	tr.hidden = []string{"SIZE", "AVG", "SRC"}

	// Act
	title := tr.paneTitle(texts, 200)

	// Assert: shared values are shown with their value, the rest by name.
	for _, want := range []string{"Ping Monitor", "SIZE 56", "SRC 10.0.0.5", "hidden: AVG"} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q lacks %q", title, want)
		}
	}
}

func TestPaneTitle_PlainWhenNothingHidden(t *testing.T) {
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", time.Millisecond)}, columnOptions{})
	if got := tr.paneTitle(texts, 200); got != " Ping Monitor " {
		t.Errorf("title = %q, want plain", got)
	}
}

func TestPaneTitle_ShortensToFit(t *testing.T) {
	// Arrange
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", time.Millisecond)}, columnOptions{SourceExplicit: true})
	tr.hidden = []string{"LAST LOSS", "LOST", "RCV", "TTL", "AVG", "SIZE", "SRC"}
	tests := []struct {
		maxWidth int
		want     string
	}{
		{200, " Ping Monitor · SIZE 56 · SRC 10.0.0.5 · hidden: LAST LOSS LOST RCV TTL AVG "},
		{70, " Ping Monitor · hidden: LAST LOSS LOST RCV TTL AVG SIZE SRC "},
		{40, " Ping Monitor · 7 columns hidden "},
		{20, " Ping Monitor "},
	}
	for _, tt := range tests {
		// Act
		got := tr.paneTitle(texts, tt.maxWidth)

		// Assert
		if got != tt.want {
			t.Errorf("maxWidth %d: title = %q, want %q", tt.maxWidth, got, tt.want)
		}
	}
}

func TestPlanColumns_ErrorColumnStaysAfterRecovery(t *testing.T) {
	// Arrange: a target fails once, then answers again.
	target := replyingTarget("10.0.0.1", 12*time.Millisecond)
	target.IncSent()
	target.OnFailure("Timeout")
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{target}, columnOptions{})
	tr.planColumns(texts, 200)
	target.IncSent()
	target.OnSuccess(12*time.Millisecond, 64)
	_, recovered := tr.renderTexts(fetchViews(tr.targets, tr.sparkPoints))

	// Act
	plan := tr.planColumns(recovered, 200)

	// Assert
	if !slices.Contains(shownNames(tr, plan), "ERR") {
		t.Errorf("ERR column vanished after recovery: %v", shownNames(tr, plan))
	}
}

func TestPlanColumns_StableWhenValuesShrink(t *testing.T) {
	// Arrange: a width where the long RTT value forces columns to hide.
	target := replyingTarget("10.0.0.1", 679842*time.Microsecond)
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{target}, columnOptions{})
	width := 60
	longPlan := tr.planColumns(texts, width)
	if len(longPlan.hidden) == 0 {
		t.Fatal("fixture hides nothing")
	}
	short := replyingTarget("10.0.0.1", 9*time.Millisecond)
	_, shortTexts := tr.renderTexts(fetchViews([]*stats.TargetStats{short}, tr.sparkPoints))

	// Act: the next tick's values are narrower.
	shortPlan := tr.planColumns(shortTexts, width)

	// Assert: hidden columns do not come back for one tick.
	if !slices.Equal(shortPlan.hidden, longPlan.hidden) {
		t.Errorf("hidden changed: %v -> %v", longPlan.hidden, shortPlan.hidden)
	}
}

func TestPlanColumns_LongHostKeepsCoreColumnsVisible(t *testing.T) {
	long := replyingTarget(strings.Repeat("very-long-label.", 8)+"example.com", 12*time.Millisecond)
	long.SetIP("10.0.0.9")
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{long}, columnOptions{})

	plan := tr.planColumns(texts, 120)
	if !plan.ok {
		t.Fatal("plan did not fit")
	}
	for _, name := range []string{"DST", "LOSS%", "RTT", "JIT"} {
		if !slices.Contains(shownNames(tr, plan), name) {
			t.Errorf("core column %s is hidden", name)
		}
	}
	if plan.widths[0] >= len(texts[0][columnIndex(tr.cols, "DST")]) {
		t.Error("long DST should shrink to fit alongside the core columns")
	}
}

func TestSparklineWidth_SingleCellGlyphs(t *testing.T) {
	target := replyingTarget("10.0.0.1", 12*time.Millisecond)
	target.IncSent()
	target.OnFailure("Timeout")
	target.IncSent()
	target.OnSuccess(680*time.Millisecond, 64)
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{target}, columnOptions{SparkPoints: 60})
	sparkCol := columnIndex(tr.cols, "RTT 60s")
	if sparkCol < 0 {
		t.Fatal("RTT 60s column absent")
	}
	if got := tr.planColumns(texts, 200); !got.ok {
		t.Fatal("plan did not fit")
	}
	if got := tr.widthSeen[sparkCol]; got != sparkWidth {
		t.Errorf("RTT 60s content width = %d, want %d", got, sparkWidth)
	}
}

func TestPlanColumns_SparklineOutlivesSupplementaryColumns(t *testing.T) {
	// Arrange: the graph pane is folded, so the sparkline stands in for it.
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", 12*time.Millisecond)}, columnOptions{SparkPoints: 60})

	// Act: the narrowest table that still fits.
	var plan columnPlan
	for width := 20; width <= 200 && !plan.ok; width++ {
		plan = tr.planColumns(texts, width)
	}
	var withSpark columnPlan
	for width := 20; width <= 200; width++ {
		if p := tr.planColumns(texts, width); slices.Contains(shownNames(tr, p), sparkColumnName) {
			withSpark = p
			break
		}
	}

	// Assert: when the sparkline first appears, every supplementary column
	// is still hidden; it is the last of them to go.
	for _, name := range []string{"SIZE", "MTU", "SRC", "LAST LOSS", "LOST", "RCV", "TTL", "AVG"} {
		if !slices.Contains(withSpark.hidden, name) {
			t.Errorf("%s shown before the sparkline: hidden %v", name, withSpark.hidden)
		}
	}
	if !slices.Contains(plan.hidden, sparkColumnName) {
		t.Errorf("at the narrowest width the sparkline should hide too: %v", plan.hidden)
	}
}

func TestPlanColumns_SpansWidthWithStableNumericColumns(t *testing.T) {
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", 12*time.Millisecond)}, columnOptions{})
	wideNumbers := slices.Clone(texts[0])
	for name, value := range map[string]string{
		"RCV": "999999", "LOST": "999999", "LOSS%": "100.0%", "RTT": "99999.99ms",
		"AVG": "10000.00ms", "JIT": "999.99ms", "SIZE": "65535", "MTU": "65535", "TTL": "255",
	} {
		wideNumbers[columnIndex(tr.cols, name)] = value
	}

	for _, tt := range []struct {
		width       int
		wantNumeric []int
	}{
		{105, []int{6, 6, 6, 10, 10, 10, 6, 6, 3}},
		{200, []int{6, 6, 6, 10, 10, 10, 6, 6, 3}},
		{300, []int{12, 12, 12, 21, 21, 21, 12, 12, 6}},
	} {
		for _, row := range [][]string{texts[0], wideNumbers} {
			plan := tr.planColumns([][]string{row}, tt.width)
			assertPingPlanWidth(t, plan, tt.width)
			if len(plan.hidden) != 0 {
				t.Fatalf("width %d: hidden columns = %v", tt.width, plan.hidden)
			}
			if got := pingNumericWidths(tr, plan); !slices.Equal(got, tt.wantNumeric) {
				t.Errorf("width %d: numeric widths = %v, want %v", tt.width, got, tt.wantNumeric)
			}
		}
	}
}

func TestPlanColumns_NumericWidthsStayPutAsTextChanges(t *testing.T) {
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", time.Millisecond)}, columnOptions{})
	dst, errCol := columnIndex(tr.cols, "DST"), columnIndex(tr.cols, "ERR")
	texts[0][errCol] = "Timeout"
	const width = 300
	before := tr.planColumns(texts, width)
	assertPingPlanWidth(t, before, width)
	want := pingNumericWidths(tr, before)
	if want[3] <= tr.cols[columnIndex(tr.cols, "RTT")].base {
		t.Fatal("fixture must give the numeric columns spare width")
	}

	for _, change := range []struct {
		col   int
		value string
	}{
		{dst, strings.Repeat("router.", 18) + "example.com"},
		{errCol, strings.Repeat("network unreachable ", 9)},
	} {
		previous := texts[0][change.col]
		texts[0][change.col] = change.value
		after := tr.planColumns(texts, width)
		texts[0][change.col] = previous
		assertPingPlanWidth(t, after, width)
		if !slices.Equal(before.active, after.active) {
			t.Fatalf("active columns changed: %v -> %v", shownNames(tr, before), shownNames(tr, after))
		}
		if got := pingNumericWidths(tr, after); !slices.Equal(got, want) {
			t.Errorf("after changing %s: numeric widths = %v, want %v", tr.cols[change.col].name, got, want)
		}
	}
}

func TestPlanColumns_GivesBackNumericPaddingBeforeHidingColumns(t *testing.T) {
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", time.Millisecond)}, columnOptions{})
	const width = 300
	before := tr.planColumns(texts, width)
	texts[0][columnIndex(tr.cols, "DST")] = strings.Repeat("x", 207)
	after := tr.planColumns(texts, width)
	assertPingPlanWidth(t, after, width)
	if !slices.Equal(before.active, after.active) {
		t.Fatalf("active columns changed: %v -> %v", shownNames(tr, before), shownNames(tr, after))
	}
	beforeNumbers := pingNumericWidths(tr, before)
	afterNumbers := pingNumericWidths(tr, after)
	if slices.Equal(beforeNumbers, afterNumbers) {
		t.Fatalf("expected to give back padding from %v", beforeNumbers)
	}
	for j, i := range after.active {
		if c := tr.cols[i]; c.fixed && after.widths[j] < c.base {
			t.Errorf("%s shrank below its base: %d < %d", c.name, after.widths[j], c.base)
		}
	}
}

func pingNumericWidths(tr *tableRenderer, plan columnPlan) []int {
	var widths []int
	for j, i := range plan.active {
		if tr.cols[i].fixed {
			widths = append(widths, plan.widths[j])
		}
	}
	return widths
}

func assertPingPlanWidth(t *testing.T, plan columnPlan, want int) {
	t.Helper()
	if !plan.ok {
		t.Fatal("plan did not fit")
	}
	total := len(plan.widths) - 1
	for _, w := range plan.widths {
		total += w
	}
	if total != want {
		t.Errorf("table spans %d cells, want %d", total, want)
	}
}

func TestSpreadProportionally(t *testing.T) {
	for _, tt := range []struct {
		name    string
		weights []int
		extra   int
		want    []int
	}{
		{"wider gets more", []int{2, 3, 5}, 20, []int{8, 13, 19}},
		{"rounding goes to first columns", []int{2, 3, 5}, 9, []int{6, 10, 13}},
		{"small extra skips zero weight", []int{0, 1, 3}, 1, []int{4, 8, 9}},
		{"single eligible column", []int{0, 1, 0}, 8, []int{4, 15, 9}},
		{"zero weights", []int{0, 0, 0}, 8, []int{4, 7, 9}},
		{"zero extra", []int{2, 3, 5}, 0, []int{4, 7, 9}},
		{"negative extra", []int{2, 3, 5}, -2, []int{4, 7, 9}},
		{"no columns", nil, 8, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			widths := []int{4, 7, 9}[:len(tt.weights)]
			spreadProportionally(widths, tt.weights, tt.extra)
			if !slices.Equal(widths, tt.want) {
				t.Errorf("widths = %v, want %v", widths, tt.want)
			}
		})
	}
}

func TestFitWidthsToAvailable(t *testing.T) {
	tests := []struct {
		name      string
		desired   []int
		minW      []int
		maxW      []int
		priority  []int
		available int
		wantFits  bool
		wantTotal int   // checked when wantFits
		wantExact []int // checked when non-nil
	}{
		{
			name:    "shrinks desired widths to the available width",
			desired: []int{10, 20, 30}, minW: []int{5, 5, 5}, maxW: []int{50, 50, 50},
			priority: []int{0, 1, 2}, available: 30, wantFits: true, wantTotal: 30,
		},
		{
			name:    "fails when available is below the sum of the minimums",
			desired: []int{10, 20, 30}, minW: []int{5, 5, 5}, maxW: []int{50, 50, 50},
			priority: []int{0, 1, 2}, available: 10, wantFits: false,
		},
		{
			name:    "grows widths into surplus space",
			desired: []int{10, 20, 30}, minW: []int{5, 5, 5}, maxW: []int{50, 50, 50},
			priority: []int{0, 1, 2}, available: 80, wantFits: true, wantTotal: 80,
		},
		{
			name:    "grows small desired widths up to the available width",
			desired: []int{3, 3, 3}, minW: []int{2, 2, 2}, maxW: []int{10, 10, 10},
			priority: []int{0, 1, 2}, available: 15, wantFits: true, wantTotal: 15,
		},
		{
			name:    "stops growing once every column is at its maximum",
			desired: []int{5}, minW: []int{1}, maxW: []int{5},
			priority: []int{0}, available: 20, wantFits: true, wantExact: []int{5},
		},
		{
			name:    "a minimum above the maximum wins",
			desired: []int{8}, minW: []int{10}, maxW: []int{5},
			priority: []int{0}, available: 10, wantFits: true, wantExact: []int{10},
		},
		{
			name:    "mismatched slice lengths do not fit",
			desired: []int{10, 20}, minW: []int{5}, maxW: []int{50, 50},
			priority: []int{0, 1}, available: 30, wantFits: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			widths, ok := fitWidthsToAvailable(tt.desired, tt.minW, tt.maxW, tt.priority, tt.priority, tt.available)
			if ok != tt.wantFits {
				t.Fatalf("fits = %v, want %v", ok, tt.wantFits)
			}
			if !ok {
				return
			}
			total := 0
			for i, w := range widths {
				total += w
				if hi := max(tt.maxW[i], tt.minW[i]); w < tt.minW[i] || w > hi {
					t.Errorf("width[%d] = %d outside [%d, %d]", i, w, tt.minW[i], hi)
				}
			}
			if tt.wantExact != nil {
				if !slices.Equal(widths, tt.wantExact) {
					t.Errorf("widths = %v, want %v", widths, tt.wantExact)
				}
				return
			}
			if total != tt.wantTotal {
				t.Errorf("total width = %d, want %d", total, tt.wantTotal)
			}
		})
	}
}
