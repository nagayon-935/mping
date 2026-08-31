package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// supervisorConfig holds the values a supervisor needs for the lifetime of
// one run() loop iteration. A fresh supervisor is created each time the main
// loop re-enters (on YAML reload), mirroring the closures it replaces.
type supervisorConfig struct {
	makePinger func(size int) pingerController
	packetSize int
	targets    []*stats.TargetStats
	interval   time.Duration
	timeout    time.Duration
	portSpecs  []pinger.PortSpec
	httpURLs   []string
	// bind is the -S source address / -I interface pair the ICMP pinger is
	// bound to; the port and HTTP checkers get the same one so a single mping
	// invocation cannot split its probes across different egress paths.
	bind         pinger.BindConfig
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
	kind  cmdKind
	reply chan error
}

// errSupervisorTerminated is returned for a command that would have started
// something after run() began tearing this iteration down.
var errSupervisorTerminated = errors.New("supervisor terminated")

// supervisor owns the pinger/traceroute/MTR/port/HTTP checker lifecycle for
// one run() loop iteration.
//
// Every field below except the channels and the snapshots is touched only by
// the command goroutine (see supervisor_loop.go), so none of them is
// synchronized. That is the point of the command loop: the components no
// longer each need their own defence against concurrent callers.
type supervisor struct {
	cfg supervisorConfig

	p           pingerController
	traceCancel context.CancelFunc
	traceDone   chan struct{} // closed when the current runTraceroutes goroutine returns
	portChecker *pinger.PortChecker
	httpChecker *pinger.HTTPChecker
	mtrEngine   *mtr.Engine
	state       supervisorState

	// Command plumbing. cmds is never closed — see do()'s comment.
	cmds         chan command
	done         chan struct{}
	loopDone     chan struct{}
	shutdownOnce sync.Once

	// Snapshots published by the loop for readers that must not block on it:
	// the render loop (httpSnap), count-limited runs (pingerSnap), tests
	// (stateSnap).
	httpSnap   atomic.Pointer[[]*stats.HTTPCheckResult]
	pingerSnap atomic.Pointer[pingerController]
	stateSnap  atomic.Int32
}

func newSupervisor(cfg supervisorConfig) *supervisor {
	return &supervisor{cfg: cfg}
}

// startTraceroutes cancels any previous traceroute goroutine, launches a new
// one, and tracks it via s.traceDone so tearDownAll can join it on shutdown
// instead of leaving it to be reaped by process exit. Caller must be running
// on the command goroutine (see supervisor_loop.go).
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
	// field on the next command-loop iteration, racing with this goroutine
	// reading it later. ctx here is only ever touched by this one goroutine.
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
		s.restartMTR(true)
		return nil

	case cmdResetPort:
		if s.state != stateRunning {
			return nil
		}
		s.restartPortChecker()
		return nil

	case cmdResetHTTP:
		if s.state != stateRunning {
			return nil
		}
		s.restartHTTPChecker()
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
//
// prev.Stop()/Wait()/Close() run here on the command goroutine, same as
// everything else handle() does; there is no mutex left to stall, and
// httpResults() no longer shares anything with this call — it reads a
// snapshot published by the loop instead. prev is nil only on the very first
// start; on every later start (cmdRestart from stateRunning always tears the
// previous pinger down via tearDownAll before calling startAll, and handle()
// only ever runs on the single command goroutine in loop()
// (supervisor_loop.go), which drains s.cmds one at a time, so a second
// concurrent cmdRestart cannot race in) prev is a pinger tearDownAll already
// stopped, so releasing it again here is an idempotent no-op rather than a
// real join — Pinger.Stop is sync.Once-guarded and Pinger.Close reuses that
// same guard (pinger.go).
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
		s.restartMTR(false)
	}
	s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
	s.httpChecker = setupHTTPChecker(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
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

// restartMTR replaces the MTR engine. It never calls TargetStats.Reset:
// that clears the ping counters too, and clearing those is the 'R' key's job
// — the key handler already resets every target before calling in here.
// Doing it a second time from this side also leaked into startAll, which
// reaches restartMTR on every (re)start, so a restart used to wipe the ping
// counters with --mtr on and keep them with --mtr off.
//
// resetStats clears the per-hop counters only, which is what the 'R' key
// wants and what startAll must not do (a plain restart keeps the counters,
// matching how --mtr off already behaves).
func (s *supervisor) restartMTR(resetStats bool) {
	if s.mtrEngine != nil {
		s.mtrEngine.Stop()
		s.mtrEngine = nil
	}
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
	s.mtrEngine = mtr.NewEngine(s.p.MTRProber(), s.cfg.targets, mtr.Config{
		OnFlap: s.onFlap,
	})
	s.mtrEngine.Start()
}

// restartPortChecker stops any existing port checker and replaces it with a
// freshly configured one.
func (s *supervisor) restartPortChecker() {
	if s.portChecker != nil {
		s.portChecker.Stop()
		s.portChecker.Wait()
	}
	s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
}

// restartHTTPChecker stops any existing HTTP checker and replaces it with a
// freshly configured one.
func (s *supervisor) restartHTTPChecker() {
	if s.httpChecker != nil {
		s.httpChecker.Stop()
		s.httpChecker.Wait()
	}
	s.httpChecker = setupHTTPChecker(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
}

func (s *supervisor) startPinger() error { return s.do(cmdStart) }
func (s *supervisor) stopAll()           { _ = s.do(cmdStop) }
func (s *supervisor) resetTrace()        { _ = s.do(cmdResetTrace) }
func (s *supervisor) resetMTR()          { _ = s.do(cmdResetMTR) }
func (s *supervisor) resetHTTP()         { _ = s.do(cmdResetHTTP) }
func (s *supervisor) resetPort()         { _ = s.do(cmdResetPort) }

// httpResults returns the current HTTP checker's results, or nil when no
// HTTP checker is active. Reads a snapshot rather than the live field: the
// render loop calls this every tick and must never block on the command
// queue.
func (s *supervisor) httpResults() []*stats.HTTPCheckResult {
	if r := s.httpSnap.Load(); r != nil {
		return *r
	}
	return nil
}

// waitPinger blocks until the current pinger finishes (used for
// count-limited runs).
func (s *supervisor) waitPinger() {
	if p := s.pingerSnap.Load(); p != nil {
		(*p).Wait()
	}
}
