package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/rivo/tview"
)

// ── buildGroupRows ────────────────────────────────────────────────────────────

func TestBuildGroupRows_NoGroups(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("a"),
		stats.NewTargetStats("b"),
	}
	rows := buildGroupRows(targets, nil)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.kind != groupRowUngrouped {
			t.Errorf("row %d: want groupRowUngrouped, got %v", i, r.kind)
		}
		if r.targetIdx != i {
			t.Errorf("row %d: want targetIdx=%d, got %d", i, i, r.targetIdx)
		}
		if r.groupIdx != -1 {
			t.Errorf("row %d: want groupIdx=-1, got %d", i, r.groupIdx)
		}
	}
}

func TestBuildGroupRows_UngroupedFirst(t *testing.T) {
	// targets: 0=ungrouped, 1+2=group G1
	targets := []*stats.TargetStats{
		stats.NewTargetStats("ungrouped"),
		stats.NewTargetStats("g1-a"),
		stats.NewTargetStats("g1-b"),
	}
	groups := []TargetGroup{{Name: "G1", Indices: []int{1, 2}}}
	rows := buildGroupRows(targets, groups)
	// expect: ungrouped(0), header(G1), target(1), target(2) = 4 rows
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d: %v", len(rows), rows)
	}
	if rows[0].kind != groupRowUngrouped || rows[0].targetIdx != 0 {
		t.Errorf("row0: want ungrouped target 0, got %+v", rows[0])
	}
	if rows[1].kind != groupRowHeader || rows[1].groupName != "G1" {
		t.Errorf("row1: want header G1, got %+v", rows[1])
	}
	if rows[2].kind != groupRowTarget || rows[2].targetIdx != 1 {
		t.Errorf("row2: want target 1, got %+v", rows[2])
	}
	if rows[3].kind != groupRowTarget || rows[3].targetIdx != 2 {
		t.Errorf("row3: want target 2, got %+v", rows[3])
	}
}

func TestBuildGroupRows_MultipleGroups(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("g1-a"), // 0
		stats.NewTargetStats("g2-a"), // 1
	}
	groups := []TargetGroup{
		{Name: "G1", Indices: []int{0}},
		{Name: "G2", Indices: []int{1}},
	}
	rows := buildGroupRows(targets, groups)
	// expect: header(G1), target(0), header(G2), target(1) = 4
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(rows))
	}
	if rows[0].kind != groupRowHeader || rows[0].groupName != "G1" {
		t.Errorf("row0: %+v", rows[0])
	}
	if rows[1].kind != groupRowTarget || rows[1].targetIdx != 0 {
		t.Errorf("row1: %+v", rows[1])
	}
	if rows[2].kind != groupRowHeader || rows[2].groupName != "G2" {
		t.Errorf("row2: %+v", rows[2])
	}
	if rows[3].kind != groupRowTarget || rows[3].targetIdx != 1 {
		t.Errorf("row3: %+v", rows[3])
	}
}

func TestBuildGroupRows_HeaderGroupIdxSet(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("h"),
	}
	groups := []TargetGroup{{Name: "G1", Indices: []int{0}}}
	rows := buildGroupRows(targets, groups)
	for _, r := range rows {
		if r.groupIdx < 0 {
			continue
		}
		if r.groupIdx != 0 {
			t.Errorf("all grouped rows should have groupIdx=0, got %d", r.groupIdx)
		}
	}
}

// TestTableRendererUpdate_GroupHeaderRendersEvenWhenMembersOffWindow guards
// the P1 fix's group-rendering risk: the visible row window is computed by
// table-row index (into groupRowMap), not target index, so a group header
// that falls inside the window must render even when most of that group's
// members fall outside it.
func TestTableRendererUpdate_GroupHeaderRendersEvenWhenMembersOffWindow(t *testing.T) {
	const perGroup = 20
	const numGroups = 3
	targets := make([]*stats.TargetStats, perGroup*numGroups)
	for i := range targets {
		targets[i] = stats.NewTargetStats(fmt.Sprintf("host%03d", i))
		targets[i].SetIP("10.0.0.1")
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}
	groups := make([]TargetGroup, numGroups)
	for g := 0; g < numGroups; g++ {
		indices := make([]int, perGroup)
		for i := range indices {
			indices[i] = g*perGroup + i
		}
		groups[g] = TargetGroup{Name: fmt.Sprintf("Group%d", g), Indices: indices}
	}

	table := tview.NewTable().SetBorders(true).SetSelectable(false, false).SetFixed(1, 1)
	tablePane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(table, 0, 1, true)
	tablePane.SetBorder(true)
	tablePane.SetRect(0, 0, 200, 30)
	vs := newViewState(tview.NewTextView())
	tr := newTableRenderer(targets, "10.0.0.1", "", 56, false, groups, table, tablePane, nil, vs)

	// groupRowMap layout (no ungrouped targets): [header G0, 20 members,
	// header G1 (rowIdx 21), 20 members, header G2, 20 members]. Scrolling
	// to rowIdx 21 centers the window on the G1 header while most of G1's
	// members (rowIdx 22-41) fall at or past the window's far edge.
	tr.table.SetOffset(21, 0)
	tr.update()

	headerTableRow := 22 // rowIdx 21 -> table row rowIdx+1
	if got := tr.table.GetCell(headerTableRow, 0).Text; got == "" {
		t.Fatalf("G1 header row (table row %d) expected populated cell, got empty", headerTableRow)
	}

	// G1's last member is rowIdx 41 -> table row 42, well past the window
	// end (21+tableMaxRows+1+rowRenderMargin = 21+10+1+5 = 37) and should
	// be empty.
	farRow := 42
	if got := tr.table.GetCell(farRow, 0).Text; got != "" {
		t.Errorf("G1's last member (table row %d, outside window) expected empty cell, got %q", farRow, got)
	}
}
