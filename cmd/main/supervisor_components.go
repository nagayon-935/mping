package main

import (
	"fmt"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// onFlap is the shared callback for route-change events.
func (s *supervisor) onFlap(host, desc string) {
	postLog(s.cfg.logCh, logLine("yellow", fmt.Sprintf("Route flap %s: %s", host, desc)))
}

// startAll runs only in stateStopped, after the previous measurement has
// been joined. A failed Start is released here and leaves the state stopped.
func (s *supervisor) startAll() error {
	s.clearFinished()
	var next pingerController
	if s.cfg.makeTargetPinger != nil {
		next = s.cfg.makeTargetPinger(s.cfg.packetSize, s.cfg.targets, s.cfg.specs)
	} else {
		next = s.cfg.makePinger(s.cfg.packetSize)
	}
	if err := next.Start(s.cfg.interval, s.cfg.timeout); err != nil {
		next.Stop()
		next.Wait()
		next.Close()
		return err
	}
	s.p = next
	if s.cfg.config.routeEnabled() {
		s.restartRoute(false)
	}
	s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
	s.httpChecker = s.setupHTTPChecker()
	s.state = stateRunning
	for _, t := range s.cfg.targets {
		t.SetMeasurementState("running")
		t.RecordEvent("started", "Measurements started")
	}
	if s.cfg.countLimited {
		s.startCountObserver(next)
	}
	return nil
}

func (s *supervisor) configureTarget(t *stats.TargetStats) {
	t.ConfigureHealth(healthPolicy(s.cfg.config))
	s.setTargetEventSink(t)
}

func (s *supervisor) setTargetEventSink(t *stats.TargetStats) {
	host := t.DisplayName()
	logCh := s.cfg.logCh
	t.SetEventSink(func(e stats.Event) {
		if e.Kind == "health" {
			postLog(logCh, fmt.Sprintf("[%s] %s: %s", e.At.Format(logTimeLayout), tview.Escape(host), tview.Escape(e.Message)))
		}
	})
}

func (s *supervisor) setupHTTPChecker() *pinger.HTTPChecker {
	if len(s.cfg.httpURLs) == 0 {
		return nil
	}
	opts, err := httpOptions(s.cfg.config)
	if err != nil {
		// Validation precedes supervisor construction.
		return nil
	}
	hc := pinger.NewHTTPCheckerWithOptions(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout, s.cfg.bind, opts)
	hc.Start()
	return hc
}

// tearDownAll stops everything in dependency order: the route engine is
// joined first (it probes through the pinger), then the pinger itself —
// Stop (signal), Wait (join), Close (release the raw socket), so the
// receiver goroutine has exited before its fd is closed.
//
// s.p, s.portChecker and s.httpChecker are deliberately NOT set to nil: the
// UI keeps showing each pane's last values after a stop instead of blanking
// them.
func (s *supervisor) tearDownAll() {
	s.stopRoute()
	if s.p != nil {
		s.p.Stop()
		s.p.Wait()
		s.p.Close()
	}
	s.stopPortChecker()
	s.stopHTTPChecker()
}

func (s *supervisor) stopRoute() {
	if s.routeEngine != nil {
		s.routeEngine.Stop()
		s.routeEngine = nil
	}
}

// restartRoute joins the outgoing engine before replacing it. resetStats
// clears per-hop counters only; cmdResetStats owns resetting ping counters.
// A plain start/restart preserves both sets of counters.
func (s *supervisor) restartRoute(resetStats bool) {
	s.stopRoute()
	// Cleared here rather than by the 'R' key handler: Stop above has joined
	// the outgoing engine's goroutines and the replacement has not started,
	// so this is the only window in which a probe reply cannot land in the
	// counters just after they are zeroed.
	if resetStats {
		for _, t := range s.cfg.targets {
			t.MTR().Reset()
		}
	}
	if s.p == nil {
		return
	}
	s.routeEngine = mtr.NewEngine(s.p.MTRProber(), s.cfg.targets, mtr.Config{
		PathOnly:   !s.cfg.config.mtr,
		DisableASN: !s.cfg.config.asnEnabled,
		DisablePTR: !s.cfg.config.ptrEnabled,
		OnFlap:     s.onFlap,
	})
	s.routeEngine.Start()
}

func (s *supervisor) stopPortChecker() {
	if s.portChecker != nil {
		s.portChecker.Stop()
		s.portChecker.Wait()
	}
}

// restartPortChecker stops any existing port checker and replaces it with a
// freshly configured one.
func (s *supervisor) restartPortChecker() {
	s.stopPortChecker()
	s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
}

func (s *supervisor) stopHTTPChecker() {
	if s.httpChecker != nil {
		s.httpChecker.Stop()
		s.httpChecker.Wait()
	}
}

// restartHTTPChecker stops any existing HTTP checker and replaces it with a
// freshly configured one.
func (s *supervisor) restartHTTPChecker() {
	s.stopHTTPChecker()
	s.httpChecker = s.setupHTTPChecker()
}
