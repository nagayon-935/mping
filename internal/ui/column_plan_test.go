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
	tr := newTableRenderer(targets, "10.0.0.5", "", 56, opts, nil, table, pane, nil, newViewState(tview.NewTextView()))
	_, texts := tr.renderTexts(fetchViews(targets))
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
	_, recovered := tr.renderTexts(fetchViews(tr.targets))

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
	_, shortTexts := tr.renderTexts(fetchViews([]*stats.TargetStats{short}))

	// Act: the next tick's values are narrower.
	shortPlan := tr.planColumns(shortTexts, width)

	// Assert: hidden columns do not come back for one tick.
	if !slices.Equal(shortPlan.hidden, longPlan.hidden) {
		t.Errorf("hidden changed: %v -> %v", longPlan.hidden, shortPlan.hidden)
	}
}

func TestPlanColumns_LongHostKeepsColumnsThatStillFit(t *testing.T) {
	// Arrange: one very long host name in a 120-column terminal.
	long := stats.NewTargetStats(strings.Repeat("very-long-label.", 8) + "example.com")
	long.SetIP("10.0.0.9")
	long.IncSent()
	long.OnSuccess(12*time.Millisecond, 64)
	tr, texts := newPlanRenderer(t, []*stats.TargetStats{long}, columnOptions{})

	// Act
	plan := tr.planColumns(texts, 120)

	// Assert: the host is cut, but AVG (the last column to go) is back.
	if !plan.ok {
		t.Fatal("plan did not fit")
	}
	if !slices.Contains(shownNames(tr, plan), "AVG") {
		t.Errorf("AVG hidden although it fits: shown %v hidden %v", shownNames(tr, plan), plan.hidden)
	}
}
