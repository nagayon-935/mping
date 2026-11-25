package main

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

type partialRegistrationPinger struct {
	*liveFakePinger
	addErr    error
	added     []*stats.TargetStats
	addresses []string
	removed   []*stats.TargetStats
}

func (p *partialRegistrationPinger) AddTarget(target *stats.TargetStats, address string) error {
	p.added = append(p.added, target)
	p.addresses = append(p.addresses, address)
	if len(p.added) == 2 {
		return p.addErr
	}
	return nil
}

func (p *partialRegistrationPinger) RemoveTarget(target *stats.TargetStats) {
	p.removed = append(p.removed, target)
}

func TestLiveReloadRollsBackPartialRegistration(t *testing.T) {
	specs := []targetSpec{{Host: "192.0.2.1"}, {Host: "192.0.2.2"}}
	targets := buildTargetsForIteration(specs, config{})
	groups := []ui.TargetGroup{{Name: "original", Indices: []int{0, 1}}}
	p := &partialRegistrationPinger{liveFakePinger: newLiveFakePinger(), addErr: errors.New("ID allocation failed")}
	s := newSupervisor(supervisorConfig{
		targets: targets, specs: specs, groups: groups,
		makePinger: func(int) pingerController { return p },
	})
	s.Start()
	t.Cleanup(s.Shutdown)
	if err := s.startPinger(); err != nil {
		t.Fatal(err)
	}
	before := targets[0].GetView()
	updated := []targetSpec{
		{Host: "new.example", PinnedIP: "192.0.2.3", Name: "new"},
		specs[1],
		{Host: "192.0.2.4"},
		{Host: "192.0.2.5"},
	}
	if applied, _, err := s.reloadLive(config{}, updated, nil); applied || err != p.addErr {
		t.Fatalf("reload = %v, %v; want false, %v", applied, err, p.addErr)
	}
	if !reflect.DeepEqual(p.addresses, []string{"192.0.2.3", "192.0.2.4"}) ||
		len(p.removed) != 1 || p.removed[0] != p.added[0] {
		t.Fatalf("registration addresses = %v, rollback = %v", p.addresses, p.removed)
	}
	set := s.liveTargets()
	if !reflect.DeepEqual(set.Targets, targets) || !reflect.DeepEqual(set.Groups, groups) ||
		!reflect.DeepEqual(s.cfg.specs, specs) || len(s.removed) != 0 ||
		!reflect.DeepEqual(targets[0].GetView(), before) {
		t.Fatal("failed registration changed membership, groups, archive or existing statistics")
	}
}

func TestLiveReloadReusesPinnedDuplicatesInOrder(t *testing.T) {
	specs := []targetSpec{
		{Host: "router.example", PinnedIP: "192.0.2.1", Name: "first"},
		{Host: "router.example", PinnedIP: "192.0.2.1", Name: "second"},
		{Host: "router.example", PinnedIP: "192.0.2.2", Name: "other"},
	}
	targets := buildTargetsForIteration(specs, config{})
	targets[0].SetIfaceMTU(9000)
	s := newSupervisor(supervisorConfig{targets: targets, specs: specs})
	s.Start()
	t.Cleanup(s.Shutdown)

	updated := []targetSpec{specs[2], specs[1], specs[0], specs[0]}
	cfg := config{healthFailures: 5, healthRecoveries: 2, healthWindow: 30 * time.Second}
	if applied, _, err := s.reloadLive(cfg, updated, nil); !applied || err != nil {
		t.Fatalf("reload = %v, %v", applied, err)
	}
	got := s.liveTargets().Targets
	if len(got) != 4 || got[0] != targets[2] || got[1] != targets[0] || got[2] != targets[1] {
		t.Fatalf("reload did not reuse duplicate targets in order: %v", got)
	}
	for i, target := range got {
		if target.DisplayName() != updated[i].display() {
			t.Errorf("target %d display name = %q, want %q", i, target.DisplayName(), updated[i].display())
		}
	}
	added := got[3]
	if added == targets[0] || added == targets[1] || added == targets[2] {
		t.Fatal("additional duplicate reused an existing identity")
	}
	view := added.GetView()
	if added.ResolveAddress() != "192.0.2.1" || view.IfaceMTU != 9000 || view.Health.WindowSeconds != 30 {
		t.Fatalf("new target address = %q, interface MTU = %d, health window = %g", added.ResolveAddress(), view.IfaceMTU, view.Health.WindowSeconds)
	}
}

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
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{a, b}, specs: []targetSpec{{Host: "same"}, {Host: "same"}}, makePinger: func(int) pingerController { return p }})
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
