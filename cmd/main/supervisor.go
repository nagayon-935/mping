package main

import (
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
// loop re-enters on a full YAML reload.
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
	countLimited bool
	logCh        chan string
}

// supervisorState tracks whether this supervisor's components are running.
// "The user pressed 's'" (stopped) and "run() is tearing this iteration
// down" (terminated) are separate states so a restart racing with the
// teardown cannot resurrect the pinger and checkers. stateTerminated is
// one-way: nothing revives a supervisor from it.
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
// components need not defend themselves against concurrent callers.
type cmdKind int

const (
	cmdStart cmdKind = iota
	cmdStop
	cmdRestart
	cmdResetRoute
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
	case cmdResetRoute:
		return "resetRoute"
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
	case cmdEditTargets:
		return "editTargets"
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

// supervisor owns the pinger/route engine/port/HTTP checker lifecycle for
// one run() loop iteration.
//
// Component references and state belong exclusively to the command loop.
// It owns Stop/Wait for the pinger and checkers, and calls the route
// engine's Stop (which joins internally). Shutdown joins the command
// loop and count observers. Readers outside the loop use atomic snapshots.
type supervisor struct {
	cfg supervisorConfig

	p              pingerController
	portChecker    *pinger.PortChecker
	httpChecker    *pinger.HTTPChecker
	routeEngine    *mtr.Engine
	state          supervisorState
	targetSnap     atomic.Pointer[targetSnapshot]
	removed        []report.Target
	removedDropped int

	// Command plumbing. cmds is never closed — see sendCmd()'s comment.
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
	for _, t := range cfg.targets {
		s.configureTarget(t)
	}
	if cfg.countLimited {
		s.finished = make(chan struct{}, 1)
	}
	return s
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
			s.markProbesFinished()
		}
		return nil
	case cmdStart:
		switch s.state {
		case stateTerminated:
			return errSupervisorTerminated
		case stateRunning:
			return nil
		}
		return s.startAll()
	case cmdStop:
		if s.state == stateRunning {
			s.stopRunning()
		}
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
	case cmdResetStats:
		if s.state != stateTerminated {
			s.resetAll()
		}
		return nil
	case cmdResetRoute:
		if s.state == stateRunning {
			s.restartRoute(true)
		}
		return nil
	case cmdResetPort:
		if s.state == stateRunning {
			s.restartPortChecker()
		}
		return nil
	case cmdResetHTTP:
		if s.state == stateRunning {
			s.restartHTTPChecker()
		}
		return nil
	case cmdTerminate:
		if s.state != stateTerminated {
			s.terminate()
		}
		return nil
	}
	return fmt.Errorf("supervisor: unknown command %d", c.kind)
}

// markProbesFinished notifies count completion once every worker of the
// current pinger has sent its last probe.
func (s *supervisor) markProbesFinished() {
	if dynamic, ok := s.livePinger(); ok && !dynamic.WorkersFinished() {
		return
	}
	select {
	case s.finished <- struct{}{}:
	default:
	}
	for _, t := range s.cfg.targets {
		t.SetMeasurementState("finished")
	}
}

// stopRunning tears every component down, keeping the statistics.
func (s *supervisor) stopRunning() {
	s.tearDownAll()
	for _, t := range s.cfg.targets {
		t.SetMeasurementState("stopped")
		t.RecordEvent("stopped", "Measurements stopped")
	}
	s.state = stateStopped
}

// resetAll clears every target's statistics. While running it also
// recreates the route, port and HTTP monitors; a reset never resumes
// stopped measurements.
func (s *supervisor) resetAll() {
	// Stop route writers before resetting their state. Ping workers keep
	// their count budget; TargetStats.Reset invalidates their old probes.
	s.stopRoute()
	for _, t := range s.cfg.targets {
		t.Reset()
	}
	if s.state != stateRunning {
		return
	}
	if s.cfg.config.routeEnabled() {
		s.restartRoute(false)
	}
	s.restartPortChecker()
	s.restartHTTPChecker()
}

// terminate ends the supervisor for good: nothing restarts it afterwards.
func (s *supervisor) terminate() {
	if s.state == stateRunning {
		s.tearDownAll()
	}
	s.state = stateTerminated
	for _, t := range s.cfg.targets {
		if t.MeasurementState() != "finished" {
			t.SetMeasurementState("stopped")
		}
	}
}

// clearFinished drops a pending count-completion notification so a membership
// or restart change is not mistaken for a run that just finished.
func (s *supervisor) clearFinished() {
	select {
	case <-s.finished:
	default:
	}
}

func (s *supervisor) startPinger() error { return s.sendCmd(command{kind: cmdStart}) }
func (s *supervisor) stopAll()           { _ = s.sendCmd(command{kind: cmdStop}) }
func (s *supervisor) resetRoute()        { _ = s.sendCmd(command{kind: cmdResetRoute}) }
func (s *supervisor) resetHTTP()         { _ = s.sendCmd(command{kind: cmdResetHTTP}) }
func (s *supervisor) resetPort()         { _ = s.sendCmd(command{kind: cmdResetPort}) }
func (s *supervisor) resetStats()        { _ = s.sendCmd(command{kind: cmdResetStats}) }

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
