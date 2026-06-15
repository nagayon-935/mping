package ui

import (
	"testing"

	"github.com/nagayon-935/mping/internal/stats"
)

// ── buildGroupRows ────────────────────────────────────────────────────────────

func TestBuildGroupRows_NoGroups(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("a"),
		stats.NewTargetStats("b"),
	}
	rows := buildGroupRows(targets, nil, nil, nil)
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
	rows := buildGroupRows(targets, groups, map[string]bool{}, nil)
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
	rows := buildGroupRows(targets, groups, map[string]bool{}, nil)
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

func TestBuildGroupRows_CollapsedHidesTargetRows(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("g1-a"),
		stats.NewTargetStats("g1-b"),
	}
	groups := []TargetGroup{{Name: "G1", Indices: []int{0, 1}}}
	rows := buildGroupRows(targets, groups, map[string]bool{"G1": true}, nil)
	// collapsed: header only = 1
	if len(rows) != 1 {
		t.Fatalf("want 1 row when collapsed, got %d", len(rows))
	}
	if rows[0].kind != groupRowHeader {
		t.Errorf("row0: want header, got %v", rows[0].kind)
	}
}

func TestBuildGroupRows_FilterSkipsUngrouped(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("alpha"),
		stats.NewTargetStats("beta"),
	}
	// Only "alpha" passes the filter
	filter := func(v stats.TargetView) bool { return v.Host == "alpha" }
	rows := buildGroupRows(targets, nil, nil, filter)
	if len(rows) != 1 {
		t.Fatalf("want 1 row after filter, got %d", len(rows))
	}
	if rows[0].targetIdx != 0 {
		t.Errorf("want targetIdx=0, got %d", rows[0].targetIdx)
	}
}

func TestBuildGroupRows_HeaderGroupIdxSet(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("h"),
	}
	groups := []TargetGroup{{Name: "G1", Indices: []int{0}}}
	rows := buildGroupRows(targets, groups, map[string]bool{}, nil)
	for _, r := range rows {
		if r.groupIdx < 0 {
			continue
		}
		if r.groupIdx != 0 {
			t.Errorf("all grouped rows should have groupIdx=0, got %d", r.groupIdx)
		}
	}
}
