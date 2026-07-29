package ui

import (
	"fmt"
	"testing"

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

	return newTableRenderer(targets, "10.0.0.1", "", 56, true, nil, table, tablePane, nil, vs)
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

// TestColumnWidthsFollowLateArrivingASN verifies the ASN column widens once
// the Cymru lookup lands. Widths feed formatCellText's truncation
// (internal/ui/tui_helpers.go), so a width frozen at startup silently
// clipped "AS15169 Google LLC" down to the header's 4 columns until the
// user resized the terminal.
//
// This drives calcColumnWidths directly (the fix). It used to drive
// calcColumnWidthsCached — the deleted function that held the bug: the
// renderer is not attached to a running tview app, so GetInnerRect reported
// width 0 on both calls, exactly the "terminal width unchanged" condition
// under which the old cache served stale widths forever.
func TestColumnWidthsFollowLateArrivingASN(t *testing.T) {
	tr := newBenchTableRenderer(t, 1)

	asnIdx := -1
	for i, c := range tr.cols {
		if c.name == "ASN" {
			asnIdx = i
			break
		}
	}
	if asnIdx < 0 {
		t.Fatal("ASN column not present; construct the renderer with asnEnabled=true")
	}

	// calcColumnWidths returns a fresh slice per call (unlike the deleted
	// calcColumnWidthsCached, which aliased the same backing slice across
	// calls); copying here is just defensive.
	before := []stats.TargetView{{Host: "example.com", IP: "192.0.2.1"}}
	widthsBefore := append([]int(nil), tr.calcColumnWidths(before)...)

	after := []stats.TargetView{{Host: "example.com", IP: "192.0.2.1", ASN: "AS15169", Org: "Google LLC"}}
	widthsAfter := append([]int(nil), tr.calcColumnWidths(after)...)

	if widthsAfter[asnIdx] <= widthsBefore[asnIdx] {
		t.Errorf("expected the ASN column to widen once ASN data arrived, got before=%d after=%d",
			widthsBefore[asnIdx], widthsAfter[asnIdx])
	}
}
