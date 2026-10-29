package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

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

// TestTableRendererUpdate_LayoutByWidth guards the P3 fix: update() builds
// the compact layout (and its per-target GetView() work) only as a fallback,
// so a terminal wide enough for the full layout must resolve to it — width 200
// matches the wide screens used by the Run tests — while a narrow one (70)
// must still fall back to compact exactly as before the P3 change.
func TestTableRendererUpdate_LayoutByWidth(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		wantCompact bool
		wantRows    int // -1: not asserted
	}{
		{"wide terminal keeps the full layout", 200, false, 4}, // header + one row per target
		{"narrow terminal falls back to compact", 70, true, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := make([]*stats.TargetStats, 3)
			for i := range targets {
				targets[i] = stats.NewTargetStats("host")
				targets[i].SetIP("10.0.0.1")
				targets[i].IncSent()
				targets[i].OnSuccess(10*time.Millisecond, 64)
			}
			tr := newTestTableRenderer(targets, tt.width)
			tr.update()

			if tr.compactLayout != tt.wantCompact {
				t.Errorf("compactLayout = %v, want %v", tr.compactLayout, tt.wantCompact)
			}
			if tt.wantRows >= 0 && tr.rowCount != tt.wantRows {
				t.Errorf("rowCount = %d, want %d", tr.rowCount, tt.wantRows)
			}
		})
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

func TestTableRendererUpdate_TallPaneRendersAllVisibleRows(t *testing.T) {
	const numTargets = 72
	targets := make([]*stats.TargetStats, numTargets)
	for i := range targets {
		targets[i] = stats.NewTargetStats(fmt.Sprintf("10.70.70.%d", i+1))
		targets[i].SetIP(fmt.Sprintf("10.70.70.%d", i+1))
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}

	app := tview.NewApplication()
	table := newPingTable()
	tablePane := newTablePane(table)
	vs := newViewState(newLogView())
	tr := newTableRenderer(targets, "10.0.0.1", "", 56, false, false, false, nil, table, tablePane, nil, vs)
	tr.selectionEnabled = true

	graph := newGraphPane(targets, time.Second)
	header := newHeader(time.Second)
	footer := tview.NewPages().AddPage("footer", newFooter(), true, true)
	layout := tview.NewFlex().SetDirection(tview.FlexRow)
	controls := newPaneControls(app, layout, header, footer, table, tablePane, func() int { return tr.rowCount }, nil, graph, vs.errorView)
	controls.onTableResize = tr.ensureRenderedForSize
	controls.rebuild()
	app.SetFocus(table)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(200, 100)

	view := controls.view()
	view.SetRect(0, 0, 200, 100)
	view.Draw(screen)

	// On a 200x100 screen with 72 targets, Ping Monitor gets ~55 lines (inner height 53 -> 25 visible rows).
	// Rows well beyond the old 16-row cap (e.g. row 25) must be populated on the initial draw.
	visible := tr.visibleDataRows()
	if visible <= 20 {
		t.Fatalf("visibleDataRows() = %d, want > 20 on a 100-line screen", visible)
	}
	for row := 1; row <= visible; row++ {
		if got := tr.table.GetCell(row, 1).Text; got == "" {
			t.Fatalf("row %d (within visible %d rows) was not rendered", row, visible)
		}
	}

	// Maximize Ping Monitor with 'z' and verify even more rows are populated immediately on Draw.
	if !controls.handle(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone)) {
		t.Fatal("expected 'z' to be handled")
	}
	view.Draw(screen)
	maxVisible := tr.visibleDataRows()
	if maxVisible <= visible {
		t.Fatalf("maximized visibleDataRows() = %d, want > %d", maxVisible, visible)
	}
	for row := 1; row <= maxVisible; row++ {
		if got := tr.table.GetCell(row, 1).Text; got == "" {
			t.Fatalf("maximized row %d (within visible %d rows) was not rendered", row, maxVisible)
		}
	}

	// Scroll selection to the very last target (#72) and verify the bottom visible rows and selection render.
	tr.selectedID = targets[numTargets-1].ID
	tr.scrollToSelection = true
	tr.update()
	if got := tr.table.GetCell(numTargets, 1).Text; got == "" {
		t.Fatalf("last row %d was not rendered when scrolled to bottom", numTargets)
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
