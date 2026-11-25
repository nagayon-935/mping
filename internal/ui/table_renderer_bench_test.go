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
// newTestTableRenderer (table_renderer_test.go) does, but with
// asnEnabled=true so the ASN column is present. It does not call SetRect on
// tablePane; calcColumnWidths recomputes unconditionally, so GetInnerRect
// reporting 0 does not affect it.
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

	return newTableRenderer(targets, sourceIPs{v4: "10.0.0.1"}, 56, columnOptions{ASN: true}, nil, table, tablePane, nil, vs)
}

// BenchmarkCalcColumnWidths documents the per-tick cost of recomputing
// dynamic column widths for every target. Widths are recomputed every tick
// so late-arriving values (ASN, Dst IP) widen their columns; that is only
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
	texts := tr.renderLayoutTexts(testColumnRows(views))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tr.calcColumnWidths(texts)
	}
}

// BenchmarkTableRendererUpdate_500Targets_Scrolled measures windowed
// rendering. Pass 1 (GetView, alert/log tracking) necessarily scans every
// target regardless of scroll position, since an off-screen host's
// loss/alert state must still be tracked. Windowing caps Pass 2's
// cell-rendering work: regardless of how far numTargets exceeds
// tableMinVisibleRows, only the visible-plus-margin window's rows are ever
// SetCell'd here (~16-21 rows), rather than all 500. It also confirms
// Pass 2 doesn't misbehave at a large scrolled offset.
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

// BenchmarkTableRendererUpdate_100Targets tracks the table refresh with
// populated target statistics and the sparkline disabled.
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

// BenchmarkTableRendererLiveUpdate updates every target before refreshing a
// scrolled Ping Monitor with one minute of RTT history.
func BenchmarkTableRendererLiveUpdate(b *testing.B) {
	const targetsCount = 500
	const points = 600
	targets := make([]*stats.TargetStats, targetsCount)
	for i := range targets {
		target := stats.NewTargetStats(fmt.Sprintf("host-%03d.example.com", i))
		target.SetIP(fmt.Sprintf("192.0.2.%d", i%254+1))
		for j := range points {
			target.IncSent()
			target.OnSuccess(time.Duration(j%60+1)*time.Millisecond, 64)
		}
		targets[i] = target
	}
	for _, width := range []int{80, 240} {
		b.Run(fmt.Sprintf("Width%d", width), func(b *testing.B) {
			table := tview.NewTable().SetBorders(true).SetFixed(1, 1)
			pane := tview.NewFlex().AddItem(table, 0, 1, true)
			pane.SetRect(0, 0, width, 30)
			vs := newViewState(tview.NewTextView())
			tr := newTableRenderer(targets, sourceIPs{v4: "192.0.2.254"}, 56, columnOptions{SparkPoints: points}, nil, table, pane, nil, vs)
			table.SetOffset(250, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, target := range targets {
					target.IncSent()
					target.OnSuccess(time.Duration(i%60+1)*time.Millisecond, 64)
				}
				tr.update()
			}
		})
	}
}
