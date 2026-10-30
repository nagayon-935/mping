package main

import (
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/ui"
)

// TestLiveReloadSwitchesRouteModeWithoutRestarting checks that a reload that
// only switches between traceroute and mtr replaces the route engine in
// place, keeping the ping counters, while turning route monitoring off still
// takes the full-rebuild path.
func TestLiveReloadSwitchesRouteModeWithoutRestarting(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"-T", "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	specs := []targetSpec{{Host: "192.0.2.1"}}
	targets := buildTargetsForIteration(specs, cfg)
	s := newSupervisor(supervisorConfig{config: cfg, specs: specs, targets: targets,
		makePinger: func(int) pingerController { return &fakePinger{} }, interval: time.Second, timeout: time.Second})
	s.Start()
	defer s.Shutdown()
	if err := s.startPinger(); err != nil {
		t.Fatal(err)
	}
	targets[0].OnSuccess(3*time.Millisecond, 64)
	engine := func() (e *mtr.Engine) {
		_ = s.editTargets(func(s *supervisor) error { e = s.routeEngine; return nil })
		return e
	}
	before := engine()

	monitor := cfg
	monitor.trace, monitor.mtr = false, true
	applied, reason, err := s.reloadLive(monitor, specs, nil)
	if err != nil || !applied || !strings.Contains(reason, "switched to mtr") {
		t.Fatalf("reload = %v %q %v, want a live switch to mtr", applied, reason, err)
	}
	if after := engine(); after == nil || after == before {
		t.Fatal("the route engine was not replaced")
	}
	set := s.liveTargets()
	if set.Route != ui.RouteMonitor {
		t.Errorf("route mode = %v, want RouteMonitor", set.Route)
	}
	if v := set.Targets[0].GetView(); v.Recv != 1 {
		t.Errorf("ping counters lost: recv = %d", v.Recv)
	}

	off := monitor
	off.mtr = false
	if ok, reason, err := s.reloadLive(off, specs, nil); ok || err != nil || !strings.Contains(reason, "traceroute/mtr") {
		t.Fatalf("turning route monitoring off = %v %q %v, want a rebuild", ok, reason, err)
	}
}
