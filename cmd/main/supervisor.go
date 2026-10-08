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
	"github.com/nagayon-935/mping/internal/report"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

// supervisorConfig holds the values a supervisor needs for the lifetime of
// one run() loop iteration. A fresh supervisor is created each time the main
// loop re-enters (on YAML reload), mirroring the closures it replaces.
type supervisorConfig struct {
	makePinger          func(size int) pingerController
	makeTargetPinger    func(int, []*stats.TargetStats, []targetSpec) pingerController
	specs               []targetSpec
	groups              []ui.TargetGroup
	config              config
	startedAt           time.Time
	collectionStartedAt time.Time
	durationLimit       time.Duration
	durationDeadline    time.Time
	sourceIPv4          string
	sourceIPv6          string
	network             string
	reservedOutputs     []string
	packetSize          int
	targets             []*stats.TargetStats
	interval            time.Duration
	timeout             time.Duration
	portSpecs           []pinger.PortSpec
	httpURLs            []string
	// bind is the -S source address / -I interface pair the ICMP pinger is
	// bound to; the port and HTTP checkers get the same one so a single mping
	// invocation cannot split its probes across different egress paths.
	bind         pinger.BindConfig
	traceEnabled bool
	mtrEnabled   bool
	countLimited bool
	logCh        chan string
}

// supervisorState tracks whether this supervisor's components are running.
// The previous code used a single `stopped bool`, which conflated "the user
// pressed 's'" with "run() is tearing this iteration down" — a restart
// racing with the teardown could therefore resurrect the pinger and
// checkers. stateTerminated is one-way: nothing revives a supervisor from it.
//
// Command       stopped           running               terminated
// start         start -> running  no-op                 error
// stop          no-op             join -> stopped       no-op
// restart       start -> running  join/start -> running error
// resetStats    clear only        clear/recreate checks no-op
// terminate     -> terminated     join -> terminated    no-op
// A reset keeps the ping instance and its remaining count budget. A restart
// keeps statistics but gives the replacement instance a fresh count budget.
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
	cmdProbesFinished
	cmdResetStats
	cmdEditTargets
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
	case cmdProbesFinished:
		return "probesFinished"
	case cmdResetStats:
		return "resetStats"
	}
	return "unknown"
}

// command is one unit of work for the supervisor. reply may be nil when the
// sender does not care about the outcome.
type command struct {
	kind   cmdKind
	reply  chan error
	edit   func(*supervisor) error
	pinger pingerController // identity of the instance reporting completion
}

// errSupervisorTerminated is returned for a command that would have started
// something after run() began tearing this iteration down.
var errSupervisorTerminated = errors.New("supervisor terminated")

// supervisor owns the pinger/traceroute/MTR/port/HTTP checker lifecycle for
// one run() loop iteration.
//
// Component references and state belong exclusively to the command loop.
// It creates and joins traceDone, owns Stop/Wait for the pinger and checkers,
// and calls MTR.Stop (which joins internally). Shutdown joins the command
// loop and count observers. Readers outside the loop use atomic snapshots.
type supervisor struct {
	cfg supervisorConfig

	p              pingerController
	traceCancel    context.CancelFunc
	traceCtx       context.Context
	traceWG        *sync.WaitGroup
	traceDone      chan struct{} // closed when the current runTraceroutes goroutine returns
	portChecker    *pinger.PortChecker
	httpChecker    *pinger.HTTPChecker
	mtrEngine      *mtr.Engine
	state          supervisorState
	traces         map[*stats.TargetStats]*traceRun
	targetSnap     atomic.Pointer[targetSnapshot]
	removed        []report.Target
	removedDropped int

	// Command plumbing. cmds is never closed — see do()'s comment.
	cmds         chan command
	done         chan struct{}
	loopDone     chan struct{}
	shutdownOnce sync.Once
	observers    sync.WaitGroup // owns count-completion observers through Shutdown
	finished     chan struct{}  // buffered count-completion notifications; nil when unlimited

	// Snapshots published by the loop for readers that must not block on it:
	// the render loop (httpSnap) and tests (pingerSnap, stateSnap).
	httpSnap   atomic.Pointer[[]*stats.HTTPCheckResult]
	pingerSnap atomic.Pointer[pingerController]
	stateSnap  atomic.Int32
}

func newSupervisor(cfg supervisorConfig) *supervisor {
	if cfg.startedAt.IsZero() {
		cfg.startedAt = time.Now().UTC()
	}
	if cfg.collectionStartedAt.IsZero() {
		cfg.collectionStartedAt = time.Now().UTC()
	}
	s := &supervisor{cfg: cfg}
	if cfg.countLimited {
		s.finished = make(chan struct{}, 1)
	}
	return s
}

// startTraceroutes cancels and joins any previous traceroute, launches a new
// one, and tracks it via s.traceDone so tearDownAll can join it on shutdown
// instead of leaving it to be reaped by process exit. Caller must be running
// on the command goroutine (see supervisor_loop.go).
func (s *supervisor) startTraceroutes(pr tracer) {
	s.stopTraceroutes()
	s.traces = make(map[*stats.TargetStats]*traceRun)
	s.traceCtx, s.traceCancel = context.WithCancel(context.Background())
	s.traceWG = &sync.WaitGroup{}
	s.traceDone = make(chan struct{})
	ctx, wg, done := s.traceCtx, s.traceWG, s.traceDone
	go func() { <-ctx.Done(); wg.Wait(); close(done) }()
	for _, t := range s.cfg.targets {
		s.startTargetTrace(pr, t)
	}
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
// concurrently with itself — the command loop guarantees that.
// Splitting it out from the loop keeps the whole state machine testable
// without starting a goroutine.
func (s *supervisor) handle(c command) error {
	switch c.kind {
	case cmdEditTargets:
		if s.state == stateTerminated {
			return errSupervisorTerminated
		}
		return c.edit(s)
	case cmdProbesFinished:
		if s.state == stateRunning && s.p == c.pinger {
			if dynamic, ok := s.p.(dynamicPinger); ok && !dynamic.WorkersFinished() {
				return nil
			}
			select {
			case s.finished <- struct{}{}:
			default:
			}
		}
		return nil
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
		for _, t := range s.cfg.targets {
			t.RecordEvent("stopped", "Measurements stopped")
		}
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

	case cmdResetStats:
		if s.state == stateTerminated {
			return nil
		}
		// Stop route writers before resetting their state. Ping workers keep
		// their count budget; TargetStats.Reset invalidates their old probes.
		s.stopTraceroutes()
		s.stopMTR()
		for _, t := range s.cfg.targets {
			t.Reset()
			if s.state == stateRunning && s.cfg.traceEnabled {
				t.SetTraceHops(nil)
			}
		}
		if s.state != stateRunning {
			return nil // a reset never resumes stopped measurements
		}
		if s.cfg.traceEnabled {
			s.startTraceroutes(s.p)
		}
		if s.cfg.mtrEnabled {
			s.restartMTR(false)
		}
		s.restartPortChecker()
		s.restartHTTPChecker()
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

// startAll runs only in stateStopped, after the previous measurement has
// been joined. A failed Start is released here and leaves the state stopped.
func (s *supervisor) startAll() error {
	select {
	case <-s.finished:
	default:
	}
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
	if s.cfg.traceEnabled {
		s.startTraceroutes(next)
	}
	if s.cfg.mtrEnabled {
		s.restartMTR(false)
	}
	s.portChecker = setupPortChecker(s.cfg.targets, s.cfg.portSpecs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
	s.httpChecker = setupHTTPChecker(s.cfg.httpURLs, s.cfg.interval, s.cfg.timeout, s.cfg.bind)
	s.state = stateRunning
	for _, t := range s.cfg.targets {
		t.RecordEvent("started", "Measurements started")
	}
	if s.cfg.countLimited {
		s.observers.Add(1)
		go func() {
			defer s.observers.Done()
			notify := func() bool {
				select {
				case s.cmds <- command{kind: cmdProbesFinished, pinger: next}:
					return true
				case <-s.done:
					return false
				}
			}
			if dynamic, ok := next.(dynamicPinger); ok {
				for {
					select {
					case <-dynamic.WorkerEvents():
						if !notify() {
							return
						}
					case <-s.done:
						return
					case <-dynamic.Done():
						return
					}
				}
			} else {
				next.WaitWorkers()
				notify()
			}
		}()
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
	s.stopTraceroutes()
	s.stopMTR()
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

func (s *supervisor) stopTraceroutes() {
	if s.traceCancel != nil {
		s.traceCancel()
		s.traceCancel = nil
	}
	for t := range s.traces {
		s.stopTargetTrace(t)
	}
	if s.traceDone != nil {
		<-s.traceDone
		s.traceDone = nil
	}
}

func (s *supervisor) stopMTR() {
	if s.mtrEngine != nil {
		s.mtrEngine.Stop()
		s.mtrEngine = nil
	}
}

// restartMTR joins the outgoing engine before replacing it. resetStats
// clears per-hop counters only; cmdResetStats owns resetting ping counters.
// A plain start/restart preserves both sets of counters.
func (s *supervisor) restartMTR(resetStats bool) {
	s.stopMTR()
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
func (s *supervisor) resetStats()        { _ = s.do(cmdResetStats) }

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
