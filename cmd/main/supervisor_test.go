package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
)

// TestHandle_CommandTable walks every (state, command) cell of the design's
// command table, so resetRoute/resetPort/resetHTTP all follow one
// rule set (in particular, no reset resurrects a component after shutdown).
func TestHandle_CommandTable(t *testing.T) {
	tests := []struct {
		name      string
		from      supervisorState
		cmd       cmdKind
		wantState supervisorState
		wantErr   error
	}{
		{"start from stopped", stateStopped, cmdStart, stateRunning, nil},
		{"stop from stopped", stateStopped, cmdStop, stateStopped, nil},
		{"restart from stopped", stateStopped, cmdRestart, stateRunning, nil},
		{"resetStats from stopped", stateStopped, cmdResetStats, stateStopped, nil},
		{"resetRoute from stopped", stateStopped, cmdResetRoute, stateStopped, nil},
		{"resetPort from stopped", stateStopped, cmdResetPort, stateStopped, nil},
		{"resetHTTP from stopped", stateStopped, cmdResetHTTP, stateStopped, nil},
		{"terminate from stopped", stateStopped, cmdTerminate, stateTerminated, nil},

		{"start from running", stateRunning, cmdStart, stateRunning, nil},
		{"stop from running", stateRunning, cmdStop, stateStopped, nil},
		{"restart from running", stateRunning, cmdRestart, stateRunning, nil},
		{"resetStats from running", stateRunning, cmdResetStats, stateRunning, nil},
		{"resetRoute from running", stateRunning, cmdResetRoute, stateRunning, nil},
		{"resetPort from running", stateRunning, cmdResetPort, stateRunning, nil},
		{"resetHTTP from running", stateRunning, cmdResetHTTP, stateRunning, nil},
		{"terminate from running", stateRunning, cmdTerminate, stateTerminated, nil},

		// Nothing may revive a terminated supervisor.
		{"start from terminated", stateTerminated, cmdStart, stateTerminated, errSupervisorTerminated},
		{"stop from terminated", stateTerminated, cmdStop, stateTerminated, nil},
		{"restart from terminated", stateTerminated, cmdRestart, stateTerminated, errSupervisorTerminated},
		{"resetStats from terminated", stateTerminated, cmdResetStats, stateTerminated, nil},
		{"resetRoute from terminated", stateTerminated, cmdResetRoute, stateTerminated, nil},
		{"resetPort from terminated", stateTerminated, cmdResetPort, stateTerminated, nil},
		{"resetHTTP from terminated", stateTerminated, cmdResetHTTP, stateTerminated, nil},
		{"terminate from terminated", stateTerminated, cmdTerminate, stateTerminated, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sup, _ := newStateTestSupervisor(t)
			t.Cleanup(func() { _ = doCmd(t, sup, cmdTerminate) })
			// Reach the starting state through real commands rather than
			// poking the field, so the fixture can't construct a state the
			// machine would never produce.
			switch tc.from {
			case stateRunning:
				if err := doCmd(t, sup, cmdStart); err != nil {
					t.Fatalf("setup cmdStart: %v", err)
				}
			case stateTerminated:
				if err := doCmd(t, sup, cmdTerminate); err != nil {
					t.Fatalf("setup cmdTerminate: %v", err)
				}
			}

			err := doCmd(t, sup, tc.cmd)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if sup.state != tc.wantState {
				t.Errorf("state = %v, want %v", sup.state, tc.wantState)
			}
		})
	}
}

// TestHandle_ResetAfterTerminateCreatesNothing is the D1 regression guard.
// Reproduction was: press 'R' then 's'. The reset goroutines landed after
// stopAll and installed a fresh route engine probing an already-Closed
// pinger.
func TestHandle_ResetAfterTerminateCreatesNothing(t *testing.T) {
	sup, _ := newStateTestSupervisor(t)
	sup.cfg.config.mtr = true
	// portSpecs/httpURLs must be non-empty so startAll actually creates a
	// port checker and an HTTP checker below — otherwise cmdResetPort/
	// cmdResetHTTP would have nothing to (wrongly) replace and the guard
	// would go untested. Both values are chosen so their check goroutines
	// never do real network I/O: the fixture target has no IP set, so
	// PortChecker.check() short-circuits on ip == "" before dialing; the
	// HTTP URL "://no-scheme" fails to parse, so http.NewRequestWithContext
	// returns an error inside HTTPChecker.check() before any request is
	// sent. The 1s interval from newStateTestSupervisor never fires during
	// this test either way.
	sup.cfg.portSpecs = []pinger.PortSpec{{Port: 443, Protocol: "tcp"}}
	sup.cfg.httpURLs = []string{"://no-scheme"}

	if err := doCmd(t, sup, cmdStart); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	if err := doCmd(t, sup, cmdTerminate); err != nil {
		t.Fatalf("cmdTerminate: %v", err)
	}

	engineAfter := sup.routeEngine
	portCheckerAfter := sup.portChecker
	httpCheckerAfter := sup.httpChecker
	if portCheckerAfter == nil || httpCheckerAfter == nil {
		t.Fatalf("fixture failed to create checkers before terminate: portChecker=%v httpChecker=%v",
			portCheckerAfter, httpCheckerAfter)
	}

	for _, k := range []cmdKind{cmdResetStats, cmdResetRoute, cmdResetPort, cmdResetHTTP} {
		if err := doCmd(t, sup, k); err != nil {
			t.Fatalf("reset %v after terminate: %v", k, err)
		}
	}

	if sup.routeEngine != engineAfter {
		t.Error("resetRoute after terminate installed a new route engine")
	}
	if sup.portChecker != portCheckerAfter {
		t.Error("resetPort after terminate installed a new port checker")
	}
	if sup.httpChecker != httpCheckerAfter {
		t.Error("resetHTTP after terminate installed a new HTTP checker")
	}
	if sup.state != stateTerminated {
		t.Errorf("state = %v, want stateTerminated", sup.state)
	}
}

// TestHandle_RestartReleasesPreviousPinger: with commands serialized, a
// superseded pinger can only appear through sequential restarts, and each
// one must still be fully released.
func TestHandle_RestartReleasesPreviousPinger(t *testing.T) {
	sup, created := newStateTestSupervisor(t)

	if err := doCmd(t, sup, cmdStart); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	if err := doCmd(t, sup, cmdRestart); err != nil {
		t.Fatalf("cmdRestart: %v", err)
	}

	all := *created
	if len(all) != 2 {
		t.Fatalf("expected 2 pingers created, got %d", len(all))
	}
	if !all[0].isReleased() {
		t.Error("superseded pinger not released (stop and close were not both called)")
	}
	if pingerController(all[1]) != sup.p {
		t.Error("expected the second pinger to be the live one")
	}
}

func TestCmdKindString_CoversEditTargets(t *testing.T) {
	if got := cmdEditTargets.String(); got != "editTargets" {
		t.Errorf("cmdEditTargets.String() = %q, want %q", got, "editTargets")
	}
}

func TestSupervisorStopResetRestartPreservesStateAndOwnership(t *testing.T) {
	sup, created := newStateTestSupervisor(t)
	sup.Start()
	defer sup.Shutdown()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	target := sup.cfg.targets[0]
	old := target.NewProbe()
	old.IncSent()
	old.OnSuccess(time.Millisecond, 64)
	sup.stopAll()
	if target.GetView().Sent != 1 || target.GetView().Recv != 1 {
		t.Fatal("stop cleared statistics")
	}
	sup.resetStats()
	if sup.stateSnapshot() != stateStopped || len(*created) != 1 || !(*created)[0].isReleased() {
		t.Fatal("reset resurrected a stopped measurement")
	}
	if old.OnFailure("late timeout") {
		t.Fatal("stopped reset accepted old probe")
	}
	if err := sup.sendCmd(command{kind: cmdRestart}); err != nil {
		t.Fatal(err)
	}
	fresh := target.NewProbe()
	fresh.IncSent()
	fresh.OnSuccess(time.Millisecond, 64)
	if err := sup.sendCmd(command{kind: cmdRestart}); err != nil {
		t.Fatal(err)
	}
	if target.GetView().Recv != 1 || len(*created) != 3 {
		t.Fatal("restart did not preserve stats/create new instance")
	}
	before := *sup.pingerSnap.Load()
	sup.resetStats()
	if *sup.pingerSnap.Load() != before || target.GetView().Sent != 0 || target.GetView().Recv != 0 {
		t.Fatal("running reset replaced ping instance or kept old statistics")
	}
	sup.Shutdown() // also stops all owned producers without a separate terminate
	if sup.stateSnapshot() != stateTerminated {
		t.Fatal("Shutdown did not terminate")
	}
	for _, p := range *created {
		if !p.isReleased() {
			t.Fatal("Shutdown leaked a pinger")
		}
	}
}

// TestSupervisorSerializesConcurrentRestarts verifies that concurrent restarts
// queue on the command loop (supervisor_loop.go) instead of racing: exactly one
// pinger is live and every earlier one was released. (The old
// TestStartPingerReleasesSupersededPinger asserted that the loser of two
// concurrent startPinger() calls was cleaned up; the command queue now makes
// that race impossible, and TestHandle_RestartReleasesPreviousPinger covers
// the sequential release path.)
func TestSupervisorSerializesConcurrentRestarts(t *testing.T) {
	sup, created := newStateTestSupervisor(t)
	sup.Start()
	defer sup.Shutdown()

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := sup.sendCmd(command{kind: cmdRestart}); err != nil {
				t.Errorf("cmdRestart: %v", err)
			}
		})
	}
	wg.Wait()

	live := sup.pingerSnap.Load()
	if live == nil {
		t.Fatal("no live pinger after restarts")
	}

	liveCount := 0
	for i, fp := range *created {
		if pingerController(fp) == *live {
			liveCount++
			continue
		}
		if !fp.isReleased() {
			t.Errorf("superseded pinger #%d leaked (stop and close were not both called)", i)
		}
	}
	if liveCount != 1 {
		t.Errorf("expected exactly 1 live pinger, got %d of %d", liveCount, len(*created))
	}
}

// newCompletingPinger returns a fakePinger whose probes complete only when
// finish() is called or the pinger is stopped, like a count-limited run.
func newCompletingPinger() *fakePinger {
	p := &fakePinger{workersDone: make(chan struct{})}
	p.onStop = p.finish
	return p
}

func TestSupervisorNotifiesEachCountCompletion(t *testing.T) {
	var created []*fakePinger
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController {
		p := newCompletingPinger()
		created = append(created, p)
		return p
	}})
	sup.Start()
	t.Cleanup(func() { _ = sup.sendCmd(command{kind: cmdTerminate}); sup.Shutdown() })
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if i > 0 {
			if err := sup.sendCmd(command{kind: cmdRestart}); err != nil {
				t.Fatal(err)
			}
		}
		created[i].finish()
		select {
		case <-sup.finished:
		case <-time.After(time.Second):
			t.Fatal("no count completion notification")
		}
		if sup.stateSnapshot() != stateRunning {
			t.Fatal("count completion must preserve other monitors")
		}
	}
	// A stale completion from the first pinger must not complete a new run.
	if err := sup.sendCmd(command{kind: cmdRestart}); err != nil {
		t.Fatal(err)
	}
	reply := make(chan error, 1)
	sup.cmds <- command{kind: cmdProbesFinished, pinger: created[0], reply: reply}
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	select {
	case <-sup.finished:
		t.Fatal("stale completion notified")
	default:
	}
	sup.stopAll()
	// The completion observer may enqueue after stop. A stale event is rejected
	// by the state check regardless of command ordering.
	sup.cmds <- command{kind: cmdProbesFinished, pinger: created[2], reply: reply}
	<-reply
	select {
	case <-sup.finished:
		t.Fatal("explicit stop notified as count completion")
	default:
	}
}

func TestSupervisorShutdownJoinsCompletionObservers(t *testing.T) {
	p := newCompletingPinger()
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController { return p }})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	sup.Shutdown()
	if !p.isReleased() || sup.stateSnapshot() != stateTerminated {
		t.Fatal("measurement still live after Shutdown")
	}
	select {
	case <-sup.loopDone:
	default:
		t.Fatal("command loop still live")
	}
	// Shutdown has already waited for observers; repeated joins must return.
	done := make(chan struct{})
	go func() { sup.observers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion observer leaked")
	}
}

// TestCountObserverDoesNotNotifyAfterShutdown pins that a completion arriving
// after Shutdown closed done is dropped rather than queued, even though cmds
// has room for it, and that Shutdown (which joins the observer) still returns.
func TestCountObserverDoesNotNotifyAfterShutdown(t *testing.T) {
	p := &fakePinger{workersDone: make(chan struct{})} // Stop does not release the workers
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController { return p }})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	shutdownDone := make(chan struct{})
	go func() { sup.Shutdown(); close(shutdownDone) }()

	<-sup.loopDone // done is closed and nothing drains cmds any more
	p.finish()     // the observer wakes with room in cmds and done closed

	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return: the completion observer is stuck")
	}
	if n := len(sup.cmds); n != 0 {
		t.Fatalf("observer queued %d command(s) after shutdown", n)
	}
}

// TestCountObserverGivesUpWhenShutdownInterruptsASend covers the other exit:
// an observer already blocked sending to a queue nobody drains must return
// once done closes.
func TestCountObserverGivesUpWhenShutdownInterruptsASend(t *testing.T) {
	p := &fakePinger{workersDone: make(chan struct{})}
	sup := newSupervisor(supervisorConfig{})
	sup.cmds = make(chan command) // unbuffered and never read
	sup.done = make(chan struct{})
	sup.startCountObserver(p)
	p.finish()

	joined := make(chan struct{})
	go func() { sup.observers.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("observer returned before shutdown although its send cannot complete")
	case <-time.After(20 * time.Millisecond):
	}
	close(sup.done)
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("the completion observer is stuck sending after shutdown")
	}
}

// recordOnePing gives the fixture target one sent-and-answered probe so a
// later assertion can tell "stats survived" from "stats were wiped".
func recordOnePing(t *testing.T, s *supervisor) {
	t.Helper()
	tgt := s.cfg.targets[0]
	tgt.IncSent()
	tgt.OnSuccess(5*time.Millisecond, 64)
}

// TestResetRoute_PreservesPingStats is the decoupling guard: replacing the
// route engine must not call Reset() on every target, which would clear the
// ping counters (and, via TargetStats.Reset, the hop stats) as a side
// effect. Clearing ping stats is the 'R' key's job — input_handler.go
// already resets every target there — so the engine swap must not do it a
// second time from inside the supervisor.
func TestResetRoute_PreservesPingStats(t *testing.T) {
	sup, _ := newStateTestSupervisor(t)
	sup.cfg.config.mtr = true
	if err := doCmd(t, sup, cmdStart); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	defer func() { _ = doCmd(t, sup, cmdTerminate) }()
	recordOnePing(t, sup)

	if err := doCmd(t, sup, cmdResetRoute); err != nil {
		t.Fatalf("cmdResetRoute: %v", err)
	}

	v := sup.cfg.targets[0].GetView()
	if v.Sent != 1 || v.Recv != 1 {
		t.Errorf("after cmdResetRoute: Sent=%d Recv=%d, want 1/1 — the route engine swap wiped the ping counters", v.Sent, v.Recv)
	}
}

// TestRestart_PreservesPingStatsRegardlessOfMTR: restartRoute is also reached
// from startAll, so a restart that reset statistics there would wipe the
// ping counters when --mtr was on and keep them when it was off — the same
// command with two different outcomes depending on an unrelated flag. Both
// cases must preserve them.
func TestRestart_PreservesPingStatsRegardlessOfMTR(t *testing.T) {
	for _, mtrEnabled := range []bool{false, true} {
		name := "mtr disabled"
		if mtrEnabled {
			name = "mtr enabled"
		}
		t.Run(name, func(t *testing.T) {
			sup, _ := newStateTestSupervisor(t)
			sup.cfg.config.mtr = mtrEnabled
			if err := doCmd(t, sup, cmdStart); err != nil {
				t.Fatalf("cmdStart: %v", err)
			}
			defer func() { _ = doCmd(t, sup, cmdTerminate) }()
			recordOnePing(t, sup)

			if err := doCmd(t, sup, cmdRestart); err != nil {
				t.Fatalf("cmdRestart: %v", err)
			}

			v := sup.cfg.targets[0].GetView()
			if v.Sent != 1 || v.Recv != 1 {
				t.Errorf("after cmdRestart: Sent=%d Recv=%d, want 1/1", v.Sent, v.Recv)
			}
		})
	}
}
