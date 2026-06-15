package ui

import (
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// makeTarget creates a TargetStats with the given host, sent/recv/loss counts
// and a constant RTT for all successful pings.
func makeTarget(host string, sent, recv, loss int, rtt time.Duration) *stats.TargetStats {
	t := stats.NewTargetStats(host)
	for i := 0; i < recv; i++ {
		t.IncSent()
		t.OnSuccess(rtt, 64)
	}
	for i := 0; i < loss; i++ {
		t.IncSent()
		t.OnFailure("timeout")
	}
	return t
}

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
	// expect: ungrouped(0), header(G1), target(1), target(2), aggregate(G1) = 5 rows
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d: %v", len(rows), rows)
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
	if rows[4].kind != groupRowAggregate || rows[4].groupName != "G1" {
		t.Errorf("row4: want aggregate G1, got %+v", rows[4])
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
	// expect: header(G1), target(0), agg(G1), header(G2), target(1), agg(G2) = 6
	if len(rows) != 6 {
		t.Fatalf("want 6 rows, got %d", len(rows))
	}
	if rows[0].kind != groupRowHeader || rows[0].groupName != "G1" {
		t.Errorf("row0: %+v", rows[0])
	}
	if rows[1].kind != groupRowTarget || rows[1].targetIdx != 0 {
		t.Errorf("row1: %+v", rows[1])
	}
	if rows[2].kind != groupRowAggregate || rows[2].groupName != "G1" {
		t.Errorf("row2: %+v", rows[2])
	}
	if rows[3].kind != groupRowHeader || rows[3].groupName != "G2" {
		t.Errorf("row3: %+v", rows[3])
	}
	if rows[4].kind != groupRowTarget || rows[4].targetIdx != 1 {
		t.Errorf("row4: %+v", rows[4])
	}
	if rows[5].kind != groupRowAggregate || rows[5].groupName != "G2" {
		t.Errorf("row5: %+v", rows[5])
	}
}

func TestBuildGroupRows_CollapsedHidesTargetRows(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("g1-a"),
		stats.NewTargetStats("g1-b"),
	}
	groups := []TargetGroup{{Name: "G1", Indices: []int{0, 1}}}
	rows := buildGroupRows(targets, groups, map[string]bool{"G1": true}, nil)
	// collapsed: header + aggregate only = 2
	if len(rows) != 2 {
		t.Fatalf("want 2 rows when collapsed, got %d", len(rows))
	}
	if rows[0].kind != groupRowHeader {
		t.Errorf("row0: want header, got %v", rows[0].kind)
	}
	if rows[1].kind != groupRowAggregate {
		t.Errorf("row1: want aggregate, got %v", rows[1].kind)
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

// ── computeGroupAggregate ─────────────────────────────────────────────────────

func TestComputeGroupAggregate_SumsPacketCounts(t *testing.T) {
	targets := []*stats.TargetStats{
		makeTarget("h1", 10, 9, 1, 10*time.Millisecond),
		makeTarget("h2", 10, 8, 2, 5*time.Millisecond),
	}
	agg := computeGroupAggregate(targets, []int{0, 1})

	if agg.Sent != 20 {
		t.Errorf("Sent: want 20, got %d", agg.Sent)
	}
	if agg.Recv != 17 {
		t.Errorf("Recv: want 17, got %d", agg.Recv)
	}
	if agg.Loss != 3 {
		t.Errorf("Loss: want 3, got %d", agg.Loss)
	}
}

func TestComputeGroupAggregate_TakesMaxRTT(t *testing.T) {
	targets := []*stats.TargetStats{
		makeTarget("fast", 5, 5, 0, 10*time.Millisecond),
		makeTarget("slow", 5, 5, 0, 50*time.Millisecond),
	}
	agg := computeGroupAggregate(targets, []int{0, 1})

	if agg.AvgRTT != 50*time.Millisecond {
		t.Errorf("AvgRTT: want 50ms (worst), got %v", agg.AvgRTT)
	}
	if agg.MaxRTT != 50*time.Millisecond {
		t.Errorf("MaxRTT: want 50ms, got %v", agg.MaxRTT)
	}
}

func TestComputeGroupAggregate_TakesMinMinRTT(t *testing.T) {
	targets := []*stats.TargetStats{
		makeTarget("h1", 5, 5, 0, 20*time.Millisecond),
		makeTarget("h2", 5, 5, 0, 5*time.Millisecond),
	}
	agg := computeGroupAggregate(targets, []int{0, 1})

	if agg.MinRTT != 5*time.Millisecond {
		t.Errorf("MinRTT: want 5ms (best minimum), got %v", agg.MinRTT)
	}
}

func TestComputeGroupAggregate_ZeroRTTWhenNoRecv(t *testing.T) {
	targets := []*stats.TargetStats{
		makeTarget("h1", 5, 0, 5, 0),
		makeTarget("h2", 5, 0, 5, 0),
	}
	agg := computeGroupAggregate(targets, []int{0, 1})

	if agg.AvgRTT != 0 {
		t.Errorf("AvgRTT: want 0 when no recv, got %v", agg.AvgRTT)
	}
	if agg.MinRTT != 0 {
		t.Errorf("MinRTT: want 0 when no recv, got %v", agg.MinRTT)
	}
}

func TestComputeGroupAggregate_SkipsOutOfBoundsIndices(t *testing.T) {
	targets := []*stats.TargetStats{makeTarget("h1", 3, 3, 0, 10*time.Millisecond)}
	// index 99 is out of bounds — should not panic
	agg := computeGroupAggregate(targets, []int{0, 99})
	if agg.Sent != 3 {
		t.Errorf("Sent: want 3, got %d", agg.Sent)
	}
}

func TestComputeGroupAggregate_TakesMaxJitter(t *testing.T) {
	t1 := stats.NewTargetStats("h1")
	t1.IncSent()
	t1.OnSuccess(10*time.Millisecond, 64)
	t1.IncSent()
	t1.OnSuccess(30*time.Millisecond, 64) // jitter ≈ |30-10|/16

	t2 := stats.NewTargetStats("h2")
	t2.IncSent()
	t2.OnSuccess(10*time.Millisecond, 64)
	t2.IncSent()
	t2.OnSuccess(100*time.Millisecond, 64) // larger jitter

	targets := []*stats.TargetStats{t1, t2}
	agg := computeGroupAggregate(targets, []int{0, 1})
	v1 := t1.GetView()
	v2 := t2.GetView()
	want := v2.Jitter
	if v1.Jitter > v2.Jitter {
		want = v1.Jitter
	}
	if agg.Jitter != want {
		t.Errorf("Jitter: want %v (max), got %v", want, agg.Jitter)
	}
}
