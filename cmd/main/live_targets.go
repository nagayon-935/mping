package main

import (
	"fmt"
	"strings"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

type dynamicPinger interface {
	AddTarget(*stats.TargetStats, string) error
	RemoveTarget(*stats.TargetStats)
	WorkerEvents() <-chan struct{}
	WorkersFinished() bool
	Done() <-chan struct{}
}

// The supervisor finds live-edit support by type assertion, so a signature
// drift would silently disable live edits instead of failing the build.
var _ dynamicPinger = (*pinger.Pinger)(nil)

type targetSnapshot struct {
	targets          []*stats.TargetStats
	specs            []targetSpec
	groups           []ui.TargetGroup
	config           config
	measurementState string
}

func (s *supervisor) publishTargets() {
	groups := make([]ui.TargetGroup, len(s.cfg.groups))
	for i, g := range s.cfg.groups {
		groups[i] = ui.TargetGroup{Name: g.Name, Indices: append([]int(nil), g.Indices...)}
	}
	s.targetSnap.Store(&targetSnapshot{targets: append([]*stats.TargetStats(nil), s.cfg.targets...), specs: append([]targetSpec(nil), s.cfg.specs...), groups: groups, config: s.cfg.config, measurementState: s.currentMeasurementState()})
}

// This runs on the command loop. A completed target does not make a newly
// added target's active worker disappear from the session state.
func (s *supervisor) currentMeasurementState() string {
	if s.state == stateTerminated || s.state == stateStopped {
		return "stopped"
	}
	for _, t := range s.cfg.targets {
		if t.MeasurementState() != "finished" {
			return "running"
		}
	}
	return "finished"
}

func (s *supervisor) measurementState() string {
	if snap := s.targetSnap.Load(); snap != nil {
		return snap.measurementState
	}
	return ""
}

func (s *supervisor) liveTargets() ui.TargetSet {
	v := s.targetSnap.Load()
	if v == nil {
		return ui.TargetSet{}
	}
	th := v.config.thresholds
	return ui.TargetSet{Targets: v.targets, Groups: v.groups, Thresholds: &th}
}

func (s *supervisor) editTargets(edit func(*supervisor) error) error {
	return s.sendCmd(command{kind: cmdEditTargets, edit: edit})
}

func (s *supervisor) registerDynamicTarget(p dynamicPinger, t *stats.TargetStats, spec targetSpec) error {
	return p.AddTarget(t, spec.resolveAddr())
}

func (s *supervisor) attachTargetMonitors(t *stats.TargetStats) {
	if s.state != stateRunning {
		t.SetMeasurementState("stopped")
		return
	}
	if s.routeEngine != nil {
		s.routeEngine.AddTarget(t)
	}
	if s.portChecker != nil {
		s.portChecker.AddTarget(t)
	}
}

func (s *supervisor) detachTargetMonitors(p dynamicPinger, t *stats.TargetStats, reason string) {
	if p != nil {
		if s.routeEngine != nil {
			s.routeEngine.RemoveTarget(t)
		}
		if s.portChecker != nil {
			s.portChecker.RemoveTarget(t)
		}
		p.RemoveTarget(t)
	}
	t.RecordEvent("removed", reason)
	s.archiveTarget(t)
}

// newLiveTargets builds and wires the targets for specs joining a running
// session. New targets use the running session's actual interface MTU,
// inherited from the first existing target.
func (s *supervisor) newLiveTargets(cfg config, specs []targetSpec) []*stats.TargetStats {
	added := buildTargetsForIteration(specs, cfg)
	for _, t := range added {
		s.configureTarget(t)
		if len(s.cfg.targets) > 0 {
			t.SetIfaceMTU(s.cfg.targets[0].GetViewWindow(0).IfaceMTU)
		}
	}
	return added
}

func (s *supervisor) addHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	return s.editTargets(func(s *supervisor) error {
		for _, spec := range s.cfg.specs {
			if spec.display() == host || spec.Host == host {
				return fmt.Errorf("host %q is already in the list", host)
			}
		}
		specs, _, err := expandTargets([]targetSpec{{Host: host}}, nil, s.cfg.config)
		if err != nil {
			return err
		}
		added := s.newLiveTargets(s.cfg.config, specs)
		if s.state == stateRunning {
			p, ok := s.p.(dynamicPinger)
			if !ok {
				return fmt.Errorf("pinger does not support live target changes")
			}
			for i, t := range added {
				if err := s.registerDynamicTarget(p, t, specs[i]); err != nil {
					for _, previous := range added[:i] {
						p.RemoveTarget(previous)
					}
					return err
				}
			}
		}
		for _, t := range added {
			s.attachTargetMonitors(t)
			t.RecordEvent("added", "Target added")
		}
		s.cfg.targets = append(append([]*stats.TargetStats(nil), s.cfg.targets...), added...)
		s.cfg.specs = append(append([]targetSpec(nil), s.cfg.specs...), specs...)
		s.clearFinished()
		return nil
	})
}

func (s *supervisor) deleteHost(host string) error {
	return s.editTargets(func(s *supervisor) error {
		var matches []int
		for i, spec := range s.cfg.specs {
			if spec.display() == host {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 {
			return fmt.Errorf("host %q not found", host)
		}
		if len(matches) > 1 {
			return fmt.Errorf("host %q identifies multiple targets; use a unique target", host)
		}
		return s.removeTarget(matches[0])
	})
}

func (s *supervisor) removeTarget(i int) error {
	if len(s.cfg.targets) <= 1 {
		return fmt.Errorf("cannot delete the last host")
	}
	t := s.cfg.targets[i]
	var dynamic dynamicPinger
	if s.state == stateRunning {
		var ok bool
		dynamic, ok = s.p.(dynamicPinger)
		if !ok {
			return fmt.Errorf("pinger does not support live target changes")
		}
	}
	s.detachTargetMonitors(dynamic, t, "Target removed")
	newSpecs := append([]targetSpec(nil), s.cfg.specs[:i]...)
	newSpecs = append(newSpecs, s.cfg.specs[i+1:]...)
	var groups []ui.TargetGroup
	for _, g := range s.cfg.groups {
		group := ui.TargetGroup{Name: g.Name}
		for _, idx := range g.Indices {
			if idx == i {
				continue
			}
			if idx > i {
				idx--
			}
			group.Indices = append(group.Indices, idx)
		}
		if len(group.Indices) > 0 {
			groups = append(groups, group)
		}
	}
	s.cfg.groups = groups
	s.cfg.specs = newSpecs
	newTargets := append([]*stats.TargetStats(nil), s.cfg.targets[:i]...)
	s.cfg.targets = append(newTargets, s.cfg.targets[i+1:]...)
	return nil
}

func (s *supervisor) deleteTargetID(id uint64) error {
	return s.editTargets(func(s *supervisor) error {
		for i, t := range s.cfg.targets {
			if t.ID() == id {
				return s.removeTarget(i)
			}
		}
		return fmt.Errorf("selected target is no longer active")
	})
}
