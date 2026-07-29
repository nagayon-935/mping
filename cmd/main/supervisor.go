package main

import (
	"context"
	"errors"
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

// supervisorState tracks whether this supervisor's components are running.
// The previous code used a single `stopped bool`, which conflated "the user
// pressed 's'" with "run() is tearing this iteration down" — a restart
// racing with the teardown could therefore resurrect the pinger and
// checkers. stateTerminated is one-way: nothing revives a supervisor from it.
type supervisorState int

const (
	// stateStopped is the zero value on purpose: a freshly constructed
	// supervisor owns nothing until cmdStart runs.
	stateStopped supervisorState = iota
	stateRunning
	stateTerminated
)

func (s supervisorState) String() string {
	switch s {
	case stateStopped:
		return "stopped"
	case stateRunning:
		return "running"
	case stateTerminated:
		return "terminated"
	}
	return "unknown"
}

// cmdKind identifies a supervisor operation. Every mutation of supervisor
// state goes through one of these, processed one at a time, so the
// components no longer need to defend themselves against concurrent callers.
type cmdKind int

const (
	cmdStart cmdKind = iota
	cmdStop
	cmdRestart
	cmdResetTrace
	cmdResetMTR
	cmdResetPort
	cmdResetHTTP
	cmdTerminate
)

func (k cmdKind) String() string {
	switch k {
	case cmdStart:
		return "start"
	case cmdStop:
		return "stop"
	case cmdRestart:
		return "restart"
	case cmdResetTrace:
		return "resetTrace"
	case cmdResetMTR:
		return "resetMTR"
	case cmdResetPort:
		return "resetPort"
	case cmdResetHTTP:
		return "resetHTTP"
	case cmdTerminate:
		return "terminate"
	}
	return "unknown"
}

// command is one unit of work for the supervisor. reply may be nil when the
// sender does not care about the outcome.
type command struct {
	kind cmdKind
	// reply is unused until Task 2's command loop sends the result of
	// handle() on it; the field lives here now so command's shape doesn't
	// change between tasks.
	reply chan error //lint:ignore U1000 wired up by Task 2's loop()
}

// errSupervisorTerminated is returned for a command that would have started
// something after run() began tearing this iteration down.
var errSupervisorTerminated = errors.New("supervisor terminated")

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
	// state is only ever touched by whichever goroutine is executing
	// handle(). Until Task 2 introduces the command loop, callers still
	// serialize themselves with mu.
	state supervisorState
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

// handle executes one command against the current state. It is the single
// place where supervisor state changes, and it assumes it is never called
// concurrently with itself — Task 2's command loop is what guarantees that.
// Splitting it out from the loop keeps the whole state machine testable
// without starting a goroutine.
func (s *supervisor) handle(c command) error {
	switch c.kind {
	case cmdStart:
		if s.state == stateTerminated {
			return errSupervisorTerminated
		}
		if s.state == stateRunning {
			return nil
		}
		return s.startAll()

	case cmdStop:
		if s.state != stateRunning {
			return nil
		}
		s.tearDownAll()
		s.state = stateStopped
		return nil

	case cmdRestart:
		if s.state == stateTerminated {
			return errSupervisorTerminated
		}
		if s.state == stateRunning {
			s.tearDownAll()
			s.state = stateStopped
		}
		return s.startAll()

	case cmdResetTrace:
		if s.state != stateRunning {
			return nil
		}
		s.startTraceroutes(s.p)
		return nil

	case cmdResetMTR:
		if s.state != stateRunning {
			return nil
		}
		s.restartMTR()
		return nil

	case cmdResetPort:
		if s.state != stateRunning {
			return nil
		}
		if s.portChecker != nil {
			s.portChecker.Stop()
			s.portChecker.Wait()
		}
		s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout)
		return nil

	case cmdResetHTTP:
		if s.state != stateRunning {
			return nil
		}
		if s.httpChecker != nil {
			s.httpChecker.Stop()
			s.httpChecker.Wait()
		}
		s.httpChecker = setupHTTPChecker(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout)
		return nil

	case cmdTerminate:
		if s.state == stateTerminated {
			return nil
		}
		if s.state == stateRunning {
			s.tearDownAll()
		}
		s.state = stateTerminated
		return nil
	}
	return fmt.Errorf("supervisor: unknown command %d", c.kind)
}

// startAll brings up the pinger, traceroute, MTR engine and the port/HTTP
// checkers. Any pinger it supersedes is released at the end — with commands
// serialized that can only be a pinger cmdRestart already stopped, so this
// is a cheap no-op safety net rather than the race guard it used to be.
func (s *supervisor) startAll() error {
	next := s.cfg.makePinger(s.cfg.packetSize)
	if err := next.Start(s.cfg.interval, s.cfg.timeout); err != nil {
		return err
	}
	prev := s.p
	s.p = next
	if s.cfg.traceEnabled {
		s.startTraceroutes(next)
	}
	if s.cfg.mtrEnabled {
		s.restartMTR()
	}
	s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout)
	s.httpChecker = setupHTTPChecker(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout)
	s.state = stateRunning
	if prev != nil && prev != next {
		prev.Stop()
		prev.Wait()
		prev.Close()
	}
	return nil
}

// tearDownAll stops everything in dependency order: the traceroute goroutine
// is joined first (it probes through the pinger), then the MTR engine, then
// the pinger itself — Stop (signal), Wait (join), Close (release the raw
// socket), so the receiver goroutine has exited before its fd is closed.
//
// s.p, s.portChecker and s.httpChecker are deliberately NOT set to nil: the
// UI keeps showing each pane's last values after a stop instead of blanking
// them, which is the pre-existing behaviour.
func (s *supervisor) tearDownAll() {
	if s.traceCancel != nil {
		s.traceCancel()
		s.traceCancel = nil
	}
	if s.traceDone != nil {
		<-s.traceDone
		s.traceDone = nil
	}
	if s.mtrEngine != nil {
		s.mtrEngine.Stop()
		s.mtrEngine = nil
	}
	if s.p != nil {
		s.p.Stop()
		s.p.Wait()
		s.p.Close()
	}
	if s.portChecker != nil {
		s.portChecker.Stop()
		s.portChecker.Wait()
	}
	if s.httpChecker != nil {
		s.httpChecker.Stop()
		s.httpChecker.Wait()
	}
}

// restartMTR replaces the MTR engine, resetting per-target stats first.
// The 'R' key handler also calls t.Reset() on every target; that duplication
// predates this refactor and is preserved deliberately.
func (s *supervisor) restartMTR() {
	for _, t := range s.cfg.targets {
		t.Reset()
	}
	if s.mtrEngine != nil {
		s.mtrEngine.Stop()
		s.mtrEngine = nil
	}
	if s.p == nil {
		return
	}
	s.mtrEngine = mtr.NewEngine(s.p.MTRProber(), s.cfg.targets, mtr.Config{
		OnFlap: s.onFlap,
	})
	s.mtrEngine.Start()
}

func (s *supervisor) startPinger() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle(command{kind: cmdStart})
}

// setupPortAndHTTP is retained as a no-op shim for this task only: cmdStart
// now brings the checkers up together with the pinger. Task 2 deletes it
// along with its call sites.
func (s *supervisor) setupPortAndHTTP() {}

func (s *supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.handle(command{kind: cmdStop})
}

func (s *supervisor) resetTrace() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.handle(command{kind: cmdResetTrace})
}

func (s *supervisor) resetMTR() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.handle(command{kind: cmdResetMTR})
}

func (s *supervisor) resetHTTP() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.handle(command{kind: cmdResetHTTP})
}

func (s *supervisor) resetPort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.handle(command{kind: cmdResetPort})
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
