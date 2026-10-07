package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

type dynamicPinger interface {
	AddTarget(*stats.TargetStats, string, *int) error
	RemoveTarget(*stats.TargetStats)
	WorkerEvents() <-chan struct{}
	WorkersFinished() bool
	Done() <-chan struct{}
}

type targetSnapshot struct {
	targets []*stats.TargetStats
	specs   []targetSpec
	groups  []ui.TargetGroup
}

func (s *supervisor) publishTargets() {
	groups := make([]ui.TargetGroup, len(s.cfg.groups))
	for i, g := range s.cfg.groups {
		groups[i] = ui.TargetGroup{Name: g.Name, Indices: append([]int(nil), g.Indices...)}
	}
	s.targetSnap.Store(&targetSnapshot{targets: append([]*stats.TargetStats(nil), s.cfg.targets...), specs: append([]targetSpec(nil), s.cfg.specs...), groups: groups})
}

func (s *supervisor) liveTargets() ui.TargetSet {
	v := s.targetSnap.Load()
	if v == nil {
		return ui.TargetSet{}
	}
	return ui.TargetSet{Targets: v.targets, Groups: v.groups}
}

func (s *supervisor) editTargets(edit func(*supervisor) error) error {
	reply := make(chan error, 1)
	select {
	case s.cmds <- command{kind: cmdEditTargets, edit: edit, reply: reply}:
	case <-s.done:
		return errSupervisorShutDown
	}
	select {
	case err := <-reply:
		return err
	case <-s.done:
		return errSupervisorShutDown
	}
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
		added := buildTargetsForIteration(specs, s.cfg.config)
		for _, t := range added {
			// New targets use the running session's actual payload and interface MTU.
			if len(s.cfg.targets) > 0 {
				v := s.cfg.targets[0].GetView()
				t.SetIfaceMTU(v.IfaceMTU)
			}
		}
		if s.state == stateRunning {
			p, ok := s.p.(dynamicPinger)
			if !ok {
				return fmt.Errorf("pinger does not support live target changes")
			}
			for i, t := range added {
				if err := p.AddTarget(t, specs[i].resolveAddr(), nil); err != nil {
					for _, previous := range added[:i] {
						p.RemoveTarget(previous)
					}
					return err
				}
			}
			for _, t := range added {
				if s.cfg.traceEnabled {
					s.startTargetTrace(s.p, t)
				}
				if s.mtrEngine != nil {
					s.mtrEngine.AddTarget(t)
				}
				if s.portChecker != nil {
					s.portChecker.AddTarget(t)
				}
			}
		}
		s.cfg.targets = append(append([]*stats.TargetStats(nil), s.cfg.targets...), added...)
		s.cfg.specs = append(append([]targetSpec(nil), s.cfg.specs...), specs...)
		select {
		case <-s.finished:
		default:
		}
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
	if s.state == stateRunning {
		p, ok := s.p.(dynamicPinger)
		if !ok {
			return fmt.Errorf("pinger does not support live target changes")
		}
		s.stopTargetTrace(t)
		if s.mtrEngine != nil {
			s.mtrEngine.RemoveTarget(t)
		}
		if s.portChecker != nil {
			s.portChecker.RemoveTarget(t)
		}
		p.RemoveTarget(t)
	}
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

type traceRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *supervisor) startTargetTrace(p tracer, t *stats.TargetStats) {
	if s.traces == nil {
		s.traces = make(map[*stats.TargetStats]*traceRun)
	}
	parent := s.traceCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &traceRun{cancel: cancel, done: make(chan struct{})}
	s.traces[t] = r
	wg := s.traceWG
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		defer close(r.done)
		if wg != nil {
			defer wg.Done()
		}
		runTraceroutes(ctx, p, []*stats.TargetStats{t})
	}()
}

func (s *supervisor) stopTargetTrace(t *stats.TargetStats) {
	if r := s.traces[t]; r != nil {
		r.cancel()
		<-r.done
		delete(s.traces, t)
	}
}
