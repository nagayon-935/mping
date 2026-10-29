package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

func TestLiveEditsKeepStatisticsAndStoppedState(t *testing.T) {
	a, b := stats.NewTargetStats("a"), stats.NewTargetStats("b")
	a.IncSent()
	a.OnSuccess(5*time.Millisecond, 64)
	a.MTR().EnsureLen(1)
	a.MTR().RecordReply(1, "1.1.1.1", "", "", "", time.Millisecond)
	port := stats.NewPortCheckResult(443, "tcp")
	port.SetResult("Open", time.Millisecond)
	a.SetPortResults([]*stats.PortCheckResult{port})
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{a, b}, specs: []targetSpec{{Host: "a"}, {Host: "b"}}, groups: []ui.TargetGroup{{Name: "group", Indices: []int{0, 1}}}})
	s.Start()
	defer s.Shutdown()
	before := a.GetView()
	if err := s.addHost("c"); err != nil {
		t.Fatal(err)
	}
	set := s.liveTargets()
	if set.Targets[0] != a || !reflect.DeepEqual(a.GetView(), before) {
		t.Fatal("existing statistics changed")
	}
	if s.stateSnapshot() != stateStopped || set.Targets[2].GetView().Sent != 0 {
		t.Fatal("edit resumed stopped session")
	}
	if err := s.deleteHost("b"); err != nil {
		t.Fatal(err)
	}
	if got := s.liveTargets().Groups[0].Indices; !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("groups=%v", got)
	}
	if err := s.addHost("b"); err != nil {
		t.Fatal(err)
	}
	if s.liveTargets().Targets[2].ID() == b.ID() {
		t.Fatal("re-added target reused identity")
	}
	if !reflect.DeepEqual(a.GetView(), before) {
		t.Fatal("delete/re-add changed surviving statistics")
	}
}

func TestLiveEditFailureAndAmbiguousDeletionPreserveTargets(t *testing.T) {
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	p := newLiveFakePinger()
	p.failAdd = true
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{a, b}, specs: []targetSpec{{Host: "same", DSCP: "EF"}, {Host: "same", DSCP: "CS0"}}, makePinger: func(int) pingerController { return p }})
	s.Start()
	defer s.Shutdown()
	if err := s.startPinger(); err != nil {
		t.Fatal(err)
	}
	if err := s.addHost("new"); err == nil {
		t.Fatal("expected failure")
	}
	if err := s.deleteHost("same"); err == nil {
		t.Fatal("ambiguous target deleted")
	}
	if got := s.liveTargets().Targets; len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatal("failed edit changed active targets")
	}
}

func TestDeletingDuplicateOccurrencePreservesExactGroup(t *testing.T) {
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{stats.NewTargetStats("same"), stats.NewTargetStats("same"), stats.NewTargetStats("other")}, specs: []targetSpec{{Host: "same"}, {Host: "same"}, {Host: "other"}}, groups: []ui.TargetGroup{{Name: "first", Indices: []int{0}}, {Name: "second", Indices: []int{1}}}})
	if err := s.removeTarget(0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.cfg.groups, []ui.TargetGroup{{Name: "second", Indices: []int{0}}}) {
		t.Fatalf("wrong duplicate retained: %v", s.cfg.groups)
	}
}
