package main

import (
	"reflect"
	"testing"
	"time"

	ui "github.com/nagayon-935/mping/internal/ui"
)

func TestResolveAllPreservesZoneAndGroups(t *testing.T) {
	specs := []targetSpec{{Host: "fe80::1%en0", DSCP: "EF"}, {Host: "::1"}}
	groups := []ui.TargetGroup{{Name: "local", Indices: []int{0, 1}}}
	got, gotGroups, err := expandTargets(specs, groups, config{resolveAll: true, ipv6Only: true, sourceAddr: "::1"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, specs) || !reflect.DeepEqual(gotGroups, groups) {
		t.Fatalf("zone/group/DSCP changed: %+v %+v", got, gotGroups)
	}
}

func TestSupervisorStopResetRestartPreservesStateAndOwnership(t *testing.T) {
	sup, created := newStateTestSupervisor(t)
	sup.Start()
	defer sup.Shutdown()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	target := sup.cfg.targets[0]
	old := target.NewProbe()
	old.IncSent()
	old.OnSuccess(time.Millisecond, 64, 0)
	sup.stopAll()
	if target.GetView().Sent != 1 || target.GetView().Recv != 1 {
		t.Fatal("stop cleared statistics")
	}
	sup.resetStats()
	if sup.stateSnapshot() != stateStopped || len(*created) != 1 || !(*created)[0].isReleased() {
		t.Fatal("reset resurrected a stopped measurement")
	}
	if old.OnFailure("late timeout") {
		t.Fatal("stopped reset accepted old probe")
	}
	if err := sup.do(cmdRestart); err != nil {
		t.Fatal(err)
	}
	fresh := target.NewProbe()
	fresh.IncSent()
	fresh.OnSuccess(time.Millisecond, 64, 0)
	if err := sup.do(cmdRestart); err != nil {
		t.Fatal(err)
	}
	if target.GetView().Recv != 1 || len(*created) != 3 {
		t.Fatal("restart did not preserve stats/create new instance")
	}
	before := *sup.pingerSnap.Load()
	sup.resetStats()
	if *sup.pingerSnap.Load() != before || target.GetView().Sent != 0 || target.GetView().Recv != 0 {
		t.Fatal("running reset replaced ping instance or kept old statistics")
	}
	sup.Shutdown() // also stops all owned producers without a separate terminate
	if sup.stateSnapshot() != stateTerminated {
		t.Fatal("Shutdown did not terminate")
	}
	for _, p := range *created {
		if !p.isReleased() {
			t.Fatal("Shutdown leaked a pinger")
		}
	}
}

func TestSupervisorShutdownJoinsCompletionObservers(t *testing.T) {
	p := newCompletingPinger()
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController { return p }})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	sup.Shutdown()
	if !p.isReleased() || sup.stateSnapshot() != stateTerminated {
		t.Fatal("measurement still live after Shutdown")
	}
	select {
	case <-sup.loopDone:
	default:
		t.Fatal("command loop still live")
	}
	// Shutdown has already waited for observers; repeated joins must return.
	done := make(chan struct{})
	go func() { sup.observers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion observer leaked")
	}
}
