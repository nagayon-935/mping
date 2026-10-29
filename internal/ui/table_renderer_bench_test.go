package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// newBenchTableRenderer builds a tableRenderer the same way
// newTestTableRenderer (table_render_test.go) does, but with asnEnabled=true
// so the ASN column is present. It deliberately does not call SetRect on
// tablePane, which used to matter for exercising the now-deleted width
// cache (calcColumnWidthsCached invalidated only when the terminal width
// changed); GetInnerRect reporting 0 either way no longer affects
// calcColumnWidths, which recomputes unconditionally.
func newBenchTableRenderer(tb testing.TB, targetCount int) *tableRenderer {
	tb.Helper()

	targets := make([]*stats.TargetStats, targetCount)
	for i := range targets {
		targets[i] = stats.NewTargetStats(fmt.Sprintf("host-%03d", i))
		targets[i].SetIP("10.0.0.1")
	}

	table := tview.NewTable().SetBorders(true).SetSelectable(false, false).SetFixed(1, 1)
	tablePane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(table, 0, 1, true)
	tablePane.SetBorder(true).SetTitle(" Ping Monitor ").SetBorderColor(tcell.ColorWhite)
	// No SetRect: calcColumnWidths doesn't consult tablePane's rect at all,
	// so this only matters if a future caller of this helper needs it.

	errorView := tview.NewTextView()
	vs := newViewState(errorView)

	return newTableRenderer(targets, "10.0.0.1", "", 56, true, false, false, nil, table, tablePane, nil, vs)
}

// BenchmarkCalcColumnWidths documents the per-tick cost of recomputing
// dynamic column widths for every target. A previous cached version skipped
// this unless the terminal was resized, which left the ASN and Dst IP
// columns stuck at their startup width. Recomputing every tick is only
// acceptable while this stays well under the refresh interval.
func BenchmarkCalcColumnWidths(b *testing.B) {
	const targetCount = 200

	views := make([]stats.TargetView, targetCount)
	for i := range views {
		views[i] = stats.TargetView{
			Host: fmt.Sprintf("host-%03d.example.com", i),
			IP:   fmt.Sprintf("192.0.2.%d", i%256),
			ASN:  "AS15169",
			Org:  "Google LLC",
		}
	}

	tr := newBenchTableRenderer(b, targetCount)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tr.calcColumnWidths(views)
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
		for j := range 500 {
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
