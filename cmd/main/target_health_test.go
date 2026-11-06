package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func TestIterationHealthIsNotReconfiguredAfterPMTU(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"--health-failures=2", "--health-window=30s", "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.mtuEnabled = true
	specs := []targetSpec{{Host: "192.0.2.1", Name: "router"}}
	before := stats.Generation()
	targets := buildTargetsForIteration(specs, cfg)
	if got := stats.Generation() - before; got != 2 {
		t.Fatalf("target construction advanced generation by %d, want 2 (health and DNS)", got)
	}
	target := targets[0]
	health := target.GetView().Health
	if !health.Configured || health.Status != "pending" || health.EvaluatedAt.IsZero() || health.Since.IsZero() || health.WindowSeconds != 30 {
		t.Fatalf("health unavailable before PMTU: %+v", health)
	}
	setupPMTU(func(int) pingerController {
		if got := target.GetView().Health; !reflect.DeepEqual(got, health) {
			t.Errorf("health at PMTU startup = %+v, want %+v", got, health)
		}
		return &fakePinger{discoverMTU: 1472}
	}, cfg, 1500, targets, io.Discard)

	logs := make(chan string, 16)
	before = stats.Generation()
	newSupervisor(supervisorConfig{config: cfg, specs: specs, targets: targets, logCh: logs})
	if got := stats.Generation() - before; got != 0 {
		t.Errorf("supervisor construction advanced generation by %d, want 0", got)
	}
	if got := target.GetView().Health; !reflect.DeepEqual(got, health) {
		t.Errorf("supervisor changed health timestamps/state: got %+v, want %+v", got, health)
	}
	for range 2 {
		target.OnFailure("timeout")
	}
	if got := target.GetView().Health; !got.Unavailable {
		t.Errorf("configured failure threshold was lost: %+v", got)
	}
	found := false
	for len(logs) > 0 {
		if strings.Contains(<-logs, "router: crit: 2 consecutive failures") {
			found = true
		}
	}
	if !found {
		t.Fatal("supervisor did not attach the health event sink")
	}
}

func TestNewLiveTargetsConfigureHealthOnce(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"--health-window=30s", "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	s := newSupervisor(supervisorConfig{config: cfg, targets: buildTargetsForIteration([]targetSpec{{Host: "192.0.2.1"}}, cfg)})
	before := stats.Generation()
	added := s.newLiveTargets(cfg, []targetSpec{{Host: "192.0.2.2"}})
	if got := stats.Generation() - before; got != 3 {
		t.Errorf("live target construction advanced generation by %d, want 3 (health, DNS and interface MTU)", got)
	}
	if h := added[0].GetView().Health; !h.Configured || h.WindowSeconds != 30 || h.EvaluatedAt.IsZero() {
		t.Errorf("live target health = %+v", h)
	}
}

func TestLiveReloadConfiguresHealthOncePerTarget(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	specs := []targetSpec{{Host: "192.0.2.1"}}
	targets := buildTargetsForIteration(specs, cfg)
	s := newSupervisor(supervisorConfig{config: cfg, specs: specs, targets: targets})
	s.Start()
	defer s.Shutdown()
	oldHealth := targets[0].GetView().Health
	updated := cfg
	updated.healthWindow = 30 * time.Second
	before := stats.Generation()
	if applied, _, err := s.reloadLive(updated, append(specs, targetSpec{Host: "192.0.2.2"}), nil); !applied || err != nil {
		t.Fatalf("reload = %v, %v", applied, err)
	}
	// One health update for the reused target, and health, DNS, interface
	// MTU, stopped state and the added event for the new target.
	if got := stats.Generation() - before; got != 6 {
		t.Errorf("reload advanced generation by %d, want 6", got)
	}
	for i, target := range s.liveTargets().Targets {
		h := target.GetView().Health
		if !h.Configured || h.WindowSeconds != 30 || h.EvaluatedAt.IsZero() {
			t.Errorf("target %d health after reload = %+v", i, h)
		}
		if i == 0 && (!h.EvaluatedAt.After(oldHealth.EvaluatedAt) || !h.Since.Equal(oldHealth.Since)) {
			t.Errorf("policy reload timestamps = %+v, before %+v", h, oldHealth)
		}
	}
}
