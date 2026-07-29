package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// supervisorConfig holds the values a supervisor needs for the lifetime of
// one run() loop iteration. A fresh supervisor is created each time the main
// loop re-enters (on YAML reload), mirroring the closures it replaces.
type supervisorConfig struct {
	makePinger   func(size int) pingerController
	packetSize   int
	targets      []*stats.TargetStats
	interval     time.Duration
	timeout      time.Duration
	portSpecs    []pinger.PortSpec
	httpURLs     []string
	traceEnabled bool
	mtrEnabled   bool
	logCh        chan string
}

// supervisor owns the pinger/traceroute/MTR/port/HTTP checker lifecycle for
// one run() loop iteration, and the mutex guarding their shared state
// (equivalent to run()'s former pMu). TD-22②: extracted out of run() as a
// behavior-preserving move.
type supervisor struct {
	cfg supervisorConfig

	mu          sync.Mutex
	p           pingerController
	traceCancel context.CancelFunc
	traceDone   chan struct{} // closed when the current runTraceroutes goroutine returns
	portChecker *pinger.PortChecker
	httpChecker *pinger.HTTPChecker
	mtrEngine   *mtr.Engine
	// stopped records that stopAll has torn this supervisor down, so a
	// reset racing with shutdown doesn't install a checker nobody will
	// stop (see resetChecker). Cleared by startPinger/setupPortAndHTTP on
	// restart. Guarded by mu.
	stopped bool
}

func newSupervisor(cfg supervisorConfig) *supervisor {
	return &supervisor{cfg: cfg}
}

// startTraceroutes cancels any previous traceroute goroutine, launches a new
// one, and tracks it via s.traceDone so stopPinger can join it on shutdown
// instead of leaving it to be reaped by process exit. Caller must hold s.mu.
func (s *supervisor) startTraceroutes(pr tracer) {
	if s.traceCancel != nil {
		s.traceCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.traceCancel = cancel
	done := make(chan struct{})
	s.traceDone = done
	// Pass the local ctx rather than storing it in a shared field: a
	// concurrent resetTrace()/startPinger() call would reassign a shared
	// field under s.mu, racing with this goroutine reading it later. ctx
	// here is only ever touched by this one goroutine.
	go func() {
		defer close(done)
		runTraceroutes(ctx, pr, s.cfg.targets)
	}()
}

// onFlap is the shared callback for MTR route-flap events.
func (s *supervisor) onFlap(host, desc string) {
	select {
	case s.cfg.logCh <- fmt.Sprintf("[yellow][%s] Route flap %s: %s[-]",
		time.Now().Format("15:04:05"), host, desc):
	default:
	}
}

// startPinger creates and starts a new pinger, wiring up traceroute/MTR when
// enabled. next.Start() stays outside s.mu because it opens raw sockets and
// spawns goroutines while touching no supervisor state; the swap itself is
// done under s.mu, and any pinger it supersedes is released afterwards —
// outside s.mu, since Wait() can block for up to pinger's resolveTimeout and
// holding mu that long would stall httpResults() and freeze the TUI.
func (s *supervisor) startPinger() error {
	next := s.cfg.makePinger(s.cfg.packetSize)
	if err := next.Start(s.cfg.interval, s.cfg.timeout); err != nil {
		return err
	}
	s.mu.Lock()
	prev := s.p
	s.stopped = false
	if s.cfg.traceEnabled {
		s.startTraceroutes(next)
	}
	if s.cfg.mtrEnabled {
		if s.mtrEngine != nil {
			s.mtrEngine.Stop()
		}
		s.mtrEngine = mtr.NewEngine(next.MTRProber(), s.cfg.targets, mtr.Config{
			OnFlap: s.onFlap,
		})
		s.mtrEngine.Start()
	}
	s.p = next
	s.mu.Unlock()

	// Release whatever we just superseded. In the normal OnRestart flow
	// stopAll already stopped it, so this is a cheap no-op (Stop and Close
	// are idempotent). In a concurrent double-start it is what keeps the
	// loser's raw socket and worker goroutines from leaking.
	if prev != nil && prev != next {
		prev.Stop()
		prev.Wait()
		prev.Close()
	}
	return nil
}

// setupPortAndHTTP starts the port and HTTP checkers (each a no-op when its
// spec list is empty).
func (s *supervisor) setupPortAndHTTP() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout)
	s.httpChecker = setupHTTPChecker(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout)
}

// stopPinger stops, in order, the traceroute goroutine (joined), the MTR
// engine, and the pinger itself — Stop() (signal), Wait() (join), then
// Close() (release the raw ICMP socket). Close is called only after Wait
// returns, so the receiver goroutine is guaranteed to have already exited
// before its socket fd is closed out from under it. Without this, YAML
// reloads and add/delete-host swaps left each old pinger's raw socket for
// the GC finalizer to close non-deterministically instead of releasing it
// immediately. Safe to call multiple times (Close is idempotent, see
// pinger.Pinger.Close).
func (s *supervisor) stopPinger() {
	s.mu.Lock()
	if s.traceCancel != nil {
		s.traceCancel()
	}
	curTraceDone := s.traceDone
	curEngine := s.mtrEngine
	cur := s.p
	s.mu.Unlock()
	if curTraceDone != nil {
		<-curTraceDone
	}
	if curEngine != nil {
		curEngine.Stop()
	}
	if cur != nil {
		cur.Stop()
		cur.Wait()
		cur.Close()
	}
}

// stopAll stops the pinger (and trace/MTR) followed by the port and HTTP
// checkers. Called from multiple sites (OnStop, OnRestart, error path,
// normal cleanup) and must be safe both to call more than once and to call
// concurrently: Pinger.Stop, PortChecker.Stop and HTTPChecker.Stop all
// guard their close/cancel with sync.Once.
func (s *supervisor) stopAll() {
	s.stopPinger()
	s.mu.Lock()
	curPort := s.portChecker
	curHTTP := s.httpChecker
	s.mu.Unlock()
	if curPort != nil {
		curPort.Stop()
		curPort.Wait()
	}
	if curHTTP != nil {
		curHTTP.Stop()
		curHTTP.Wait()
	}
}

func (s *supervisor) resetTrace() {
	s.mu.Lock()
	cur := s.p
	s.startTraceroutes(cur)
	s.mu.Unlock()
}

func (s *supervisor) resetMTR() {
	for _, t := range s.cfg.targets {
		t.Reset()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	curEngine := s.mtrEngine
	if curEngine != nil {
		curEngine.Stop()
	}
	cur := s.p
	if cur == nil {
		return
	}
	s.mtrEngine = mtr.NewEngine(cur.MTRProber(), s.cfg.targets, mtr.Config{
		OnFlap: s.onFlap,
	})
	s.mtrEngine.Start()
}

func (s *supervisor) resetHTTP() {
	resetChecker(&s.mu, &s.httpChecker, func() *pinger.HTTPChecker {
		return setupHTTPChecker(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout)
	})
}

func (s *supervisor) resetPort() {
	resetChecker(&s.mu, &s.portChecker, func() *pinger.PortChecker {
		return setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout)
	})
}

// checkerStopper is implemented by *pinger.PortChecker and
// *pinger.HTTPChecker: the two supervisor-owned checkers that share an
// identical stop-then-replace reset lifecycle.
type checkerStopper interface {
	comparable
	Stop()
	Wait()
}

// resetChecker stops the current value at *field (if any) and swaps in a
// freshly created replacement, holding mu across the entire stop→create→
// assign sequence (matching resetMTR's pattern below, which already holds
// mu across mtrEngine.Stop()+NewEngine()+Start()). TD-46②: this closes the
// unlocked window resetHTTP/resetPort previously had, where a concurrent
// stopAll() could read *field as nil mid-reset and skip stopping the
// newly-created checker — stopAll also takes mu (see stopAll/stopPinger
// above), so it now blocks until reset finishes and then correctly stops
// the replacement. Stop/Wait on a port/HTTP checker just joins its own
// goroutines and doesn't call back into supervisor, so holding mu across
// them cannot deadlock.
func resetChecker[T checkerStopper](mu *sync.Mutex, field *T, create func() T) {
	mu.Lock()
	defer mu.Unlock()
	cur := *field
	var zero T
	if cur != zero {
		cur.Stop()
		cur.Wait()
	}
	*field = create()
}

// httpResults returns the current HTTP checker's results, or nil when no
// HTTP checker is active.
func (s *supervisor) httpResults() []*stats.HTTPCheckResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpChecker == nil {
		return nil
	}
	return s.httpChecker.Results()
}

// waitPinger blocks until the current pinger finishes (used for
// count-limited runs).
func (s *supervisor) waitPinger() {
	s.mu.Lock()
	cur := s.p
	s.mu.Unlock()
	if cur != nil {
		cur.Wait()
	}
}
