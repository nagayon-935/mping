package main

// Characterization tests for run()'s shutdown order, written before the
// supervisor / reloadCoordinator extraction so later refactors cannot silently
// reorder or drop a step:
//   trace goroutine join -> mtr stop -> pinger stop/wait/close -> port/http
//   stop/wait -> watcher stop

import (
	"bytes"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

func TestFinishIterationSingleTerminationWithInFlightWatcher(t *testing.T) {
	path := writeHostsYAML(t, "hosts: [192.0.2.1]\n")
	cfg, hosts, fs, _, err := parseArgs([]string{"-f", path})
	if err != nil {
		t.Fatal(err)
	}
	rc := newReloadCoordinator(fs, cfg, hosts)
	sup := newSupervisor(supervisorConfig{})
	sup.cmds = make(chan command, cmdChanBuffer)
	sup.done = make(chan struct{})
	sup.loopDone = make(chan struct{})

	// Acknowledge termination commands explicitly so their count is observable.
	// The watcher below waits for loopDone, so no other command needs handling.
	terminations := make(chan int, 1)
	go func() {
		count := 0
		defer close(sup.loopDone)
		for {
			select {
			case c := <-sup.cmds:
				if c.kind != cmdTerminate {
					t.Errorf("cleanup command = %v, want terminate", c.kind)
				}
				count++
				c.reply <- nil
			case <-sup.done:
				terminations <- count
				return
			}
		}
	}()

	// Keep a parsed watcher request in flight until shutdown joins the loop.
	// Requests processed before done closes may still return terminated instead.
	inFlight := make(chan struct{})
	rc.tryLive = func(cfg config, specs []targetSpec, groups []ui.TargetGroup) (bool, string, error) {
		close(inFlight)
		<-sup.loopDone
		return sup.reloadLive(cfg, specs, groups)
	}
	sig := newReloadSignal()
	logs := make(chan string, 1)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		rc.requestFileReload(sig, path, logs)
	}()
	<-inFlight
	finishIteration(sup, func() {
		if !closed(sup.loopDone) {
			t.Error("watcher cancelled before command loop was joined")
		}
	}, watchDone)

	if got := <-terminations; got != 1 {
		t.Errorf("termination commands = %d, want 1", got)
	}
	select {
	case got := <-logs:
		if !strings.HasSuffix(got, " Reload error: supervisor shut down[-]") {
			t.Errorf("in-flight watcher log = %q, want supervisor shut down", got)
		}
	default:
		t.Error("in-flight watcher shutdown error was not logged")
	}
	if closed(sig.ch) || rc.pending != nil {
		t.Error("in-flight watcher queued a reload after shutdown")
	}
}

func TestFinishIterationTerminatesBeforeJoiningWatcher(t *testing.T) {
	for _, state := range []string{"running", "stopped", "finished"} {
		t.Run(state, func(t *testing.T) {
			var order []string
			fp := &fakePinger{
				onStop:  func() { order = append(order, "stop") },
				onWait:  func() { order = append(order, "wait") },
				onClose: func() { order = append(order, "close") },
			}
			target := stats.NewTargetStats("router")
			sup := newSupervisor(supervisorConfig{
				targets:    []*stats.TargetStats{target},
				makePinger: func(int) pingerController { return fp },
			})
			sup.Start()
			t.Cleanup(sup.Shutdown)
			if err := sup.startPinger(); err != nil {
				t.Fatal(err)
			}
			if state == "stopped" {
				sup.stopAll()
			} else if state == "finished" {
				if err := sup.editTargets(func(*supervisor) error {
					target.SetMeasurementState("finished")
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			cancelled, watchDone := make(chan struct{}), make(chan struct{})
			go func() {
				<-cancelled
				order = append(order, "watcher joined")
				close(watchDone)
			}()
			finishIteration(sup, func() {
				if !closed(sup.loopDone) || !fp.isReleased() {
					t.Error("watcher cancelled before supervisor shutdown completed")
				}
				order = append(order, "watcher cancelled")
				close(cancelled)
			}, watchDone)
			want := []string{"stop", "wait", "close", "watcher cancelled", "watcher joined"}
			if !reflect.DeepEqual(order, want) {
				t.Fatalf("cleanup order = %v, want %v", order, want)
			}
			wantState := "stopped"
			if state == "finished" {
				wantState = "finished"
			}
			if sup.stateSnapshot() != stateTerminated || target.MeasurementState() != wantState {
				t.Fatalf("supervisor = %v, target = %q", sup.stateSnapshot(), target.MeasurementState())
			}
			if err := sup.startPinger(); err != errSupervisorShutDown {
				t.Fatalf("start after cleanup = %v, want %v", err, errSupervisorShutDown)
			}
		})
	}
}

func TestRunStopRestart(t *testing.T) {
	fp := &fakePinger{}
	stubRunSeams(t, func(opts ui.RunOptions) error {
		opts.OnStop()
		if err := opts.OnRestart(); err != nil {
			t.Errorf("restart failed: %v", err)
		}
		return nil
	})
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController { return fp }

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if !fp.isStarted() || !fp.isReleased() || !fp.Waited() {
		t.Fatalf("expected pinger lifecycle, started=%v released=%v waited=%v", fp.isStarted(), fp.isReleased(), fp.Waited())
	}
}

// TestRunStop_JoinsRouteEngineBeforeStoppingPinger verifies that OnStop
// (stopAll -> tearDownAll) actually waits for the route engine's per-target
// goroutine to exit rather than just cancelling its context, and only then
// stops the pinger. The onStop hook makes the ordering check deterministic
// (not timing-based): tearDownAll() joins the engine before calling
// s.p.Stop(), so a violation can only be observed if a future refactor calls
// Stop() without first joining the route engine.
func TestRunStop_JoinsRouteEngineBeforeStoppingPinger(t *testing.T) {
	started, returned := make(chan struct{}), make(chan struct{})
	fp := &fakePinger{routeStarted: started, routeReturned: returned}
	stoppedBeforeJoin := false
	fp.onStop = func() { stoppedBeforeJoin = stoppedBeforeJoin || !closed(returned) }
	stubRunSeams(t, func(opts ui.RunOptions) error {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Error("route engine did not start")
			return nil
		}
		opts.OnStop()
		if !closed(returned) {
			t.Error("OnStop() returned before the in-flight route probe exited")
		}
		return nil
	})
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController { return fp }

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
	if !fp.isReleased() {
		t.Error("expected the pinger to be stopped and closed")
	}
	if stoppedBeforeJoin {
		t.Error("pinger.Stop() was called before the route engine had joined (shutdown order violation)")
	}
}

// TestRunStop_ClosesPingerAfterWait verifies end-to-end that OnStop
// (-> stopAll -> tearDownAll) calls Close() on the pinger, and only after
// Wait() has already returned (Stop -> Wait -> Close), so the raw socket is
// released on stop. The onClose hook makes the ordering check deterministic:
// it fires before Close() is recorded.
func TestRunStop_ClosesPingerAfterWait(t *testing.T) {
	tracker := &fakePinger{}
	closeBeforeWait := false
	tracker.onClose = func() { closeBeforeWait = closeBeforeWait || !tracker.Waited() }
	stubRunSeams(t, func(opts ui.RunOptions) error {
		opts.OnStop()
		return nil
	})
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController { return tracker }

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	if !tracker.isReleased() {
		t.Error("expected Stop() and Close() to be called")
	}
	if !tracker.Waited() {
		t.Error("expected Wait() to be called")
	}
	if closeBeforeWait {
		t.Error("Close() was called before Wait() returned (expected Stop → Wait → Close ordering)")
	}
}

// TestRunStop_NoGoroutineLeak exercises trace + port + http together and
// verifies run() doesn't leave any of their goroutines behind once it
// returns. This is the aggregate regression guard for the "shutdown race /
// goroutine leak" risk flagged for run()'s decomposition.
func TestRunStop_NoGoroutineLeak(t *testing.T) {
	stubRunSeams(t, func(opts ui.RunOptions) error {
		opts.OnStop()
		return nil
	})

	before := runtime.NumGoroutine()

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-p", "80/tcp", "-H", "http://127.0.0.1:1", "-S", "127.0.0.1", "127.0.0.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	if !pollUntil(2*time.Second, func() bool { return runtime.NumGoroutine() <= before }) {
		t.Errorf("goroutine leak after run() returned: before=%d after=%d", before, runtime.NumGoroutine())
	}
}
