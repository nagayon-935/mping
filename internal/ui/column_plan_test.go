package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
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
	texts := tr.renderLayoutTexts(tr.fetchRows(0, len(targets)))
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
			name:       "supplementary columns including automatic DNS",
			opts:       columnOptions{DNS: true},
			wantHidden: []string{"SIZE", "MTU", "SRC", "DNS", "LAST LOSS", "ERR", "LOST", "RCV", "TTL", "AVG"},
		},
		{
			name: "requested columns outlive every supplementary one",
			opts: columnOptions{ASN: true, PTR: true, MTU: true, SizeExplicit: true, SourceExplicit: true},
			wantHidden: []string{
				"LAST LOSS", "ERR", "LOST", "RCV", "TTL", "AVG",
				"SIZE", "SRC", "ASN", "PTR", "PMTU",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			target := replyingTarget("10.0.0.1", 12*time.Millisecond)
			if tt.opts.DNS {
				target.SetDNSServer("8.8.8.8")
			}
			tr, texts := newPlanRenderer(t, []*stats.TargetStats{target}, tt.opts)

			// Act: the narrowest table that still fits, where only the core
			// columns can stay.
			var plan columnPlan
			for width := 200; width >= 20; width-- {
				next := tr.planColumns(texts, width)
				if !next.ok {
					break
				}
				if !slices.Equal(next.hidden, tt.wantHidden[:min(len(next.hidden), len(tt.wantHidden))]) {
					t.Fatalf("width %d: hidden = %v, want a prefix of %v", width, next.hidden, tt.wantHidden)
				}
				plan = next
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
	row := tr.fetchRows(0, 1)[0]

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
			if text := tr.cols[i].render(row); plan.widths[j]-boxCellPadding < len(text) {
				t.Errorf("width %d: %s is %d wide, value %q cut", width, tr.cols[i].name, plan.widths[j], text)
			}
			if name := tr.cols[i].name; plan.widths[j]-boxCellPadding < len(name) {
				t.Errorf("width %d: heading %s cut to %d", width, name, plan.widths[j])
			}
		}
	}
}

func TestPlanColumns_DNSRequiresAConfiguredServer(t *testing.T) {
	target := replyingTarget("10.0.0.1", time.Millisecond)
	target.SetDNSServer("8.8.8.8")
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{target}, columnOptions{})
	if plan := tr.planColumns(texts, 200); !slices.Contains(shownNames(tr, plan), "DNS") {
		t.Fatal("DNS column missing with a configured server")
	}

	target.SetDNSServer("")
	texts = tr.renderLayoutTexts(tr.fetchRows(0, len(tr.targets)))
	plan := tr.planColumns(texts, 200)
	if slices.Contains(shownNames(tr, plan), "DNS") || slices.Contains(plan.hidden, "DNS") {
		t.Errorf("DNS should be omitted without a server, got shown %v and hidden %v", shownNames(tr, plan), plan.hidden)
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

func TestPlanColumns_StableWhenMeasurementsChange(t *testing.T) {
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", time.Millisecond)}, columnOptions{SparkPoints: 60})
	changed := slices.Clone(texts[0])
	for name, value := range map[string]string{
		"RCV": "999999", "LOST": "999999", "LOSS%": "100.0%", "RTT": "99999.99ms",
		"AVG": "10000.00ms", "JIT": "999.99ms", "SIZE": "65535", "MTU": "65535", "TTL": "255",
		"ERR": strings.Repeat("network unreachable ", 9), "LAST LOSS": "87600h59m59s ago",
		sparkColumnName: strings.Repeat("×", sparkWidth),
	} {
		changed[columnIndex(tr.cols, name)] = value
	}

	for _, width := range []int{60, 105, 140, 200, 300} {
		before := tr.planColumns(texts, width)
		after := tr.planColumns([][]string{changed}, width)
		assertPingPlanWidth(t, before, width)
		assertPingPlanWidth(t, after, width)
		if !slices.Equal(before.active, after.active) || !slices.Equal(before.hidden, after.hidden) ||
			!slices.Equal(before.widths, after.widths) {
			t.Errorf("width %d: layout changed from %+v to %+v", width, before, after)
		}
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
	assertPingPlanWidth(t, plan, 120)
	if plan.widths[0]-boxCellPadding >= len(texts[0][columnIndex(tr.cols, "DST")]) {
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
	row := tr.fetchRows(0, 1)[0]
	if got := runewidth.StringWidth(tr.cols[sparkCol].render(row)); got != sparkWidth {
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
	for _, name := range []string{"SIZE", "MTU", "SRC", "LAST LOSS", "ERR", "LOST", "RCV", "TTL", "AVG"} {
		if !slices.Contains(withSpark.hidden, name) {
			t.Errorf("%s shown before the sparkline: hidden %v", name, withSpark.hidden)
		}
	}
	if !slices.Contains(plan.hidden, sparkColumnName) {
		t.Errorf("at the narrowest width the sparkline should hide too: %v", plan.hidden)
	}
}

func TestPlanColumns_SharesSpareWidthOnlyAcrossTextColumns(t *testing.T) {
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{replyingTarget("10.0.0.1", time.Millisecond)}, columnOptions{SparkPoints: 60})
	// Source, destination, counters, timings, sparkline, settings, TTL,
	// error and last loss. Padding and separators are accounted for below.
	references := []int{8, 8, 6, 6, 6, 10, 10, 10, 16, 6, 6, 3, 20, 15}
	// SRC, DST, the sparkline, ERR and LAST LOSS share the spare width;
	// numeric columns stay at their fixed widths.
	grows := map[int]bool{0: true, 1: true, 8: true, 12: true, 13: true}
	total, textTotal := 0, 0
	for j, ref := range references {
		total += ref
		if grows[j] {
			textTotal += ref
		}
	}
	minimum := total + boxCellPadding*len(references) + len(references) - 1

	for _, spare := range []int{0, 20, total} {
		plan := tr.planColumns(texts, minimum+spare)
		assertPingPlanWidth(t, plan, minimum+spare)
		if len(plan.hidden) != 0 || len(plan.widths) != len(references) {
			t.Fatalf("spare %d: expected all columns, got %v", spare, shownNames(tr, plan))
		}
		for j, ref := range references {
			got := plan.widths[j] - boxCellPadding - ref
			lower, upper := 0, 0
			if grows[j] {
				lower = spare * ref / textTotal
				upper = lower
				if spare > 0 {
					upper++ // A remainder cell can go to this column.
				}
			}
			if got < lower || got > upper {
				t.Errorf("spare %d: %s gained %d cells, want %d to %d", spare, tr.cols[plan.active[j]].name, got, lower, upper)
			}
		}
	}
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

func testColumnRows(views []stats.TargetView) []columnRowContext {
	rows := make([]columnRowContext, len(views))
	for i, view := range views {
		rows[i].view = view
	}
	return rows
}
