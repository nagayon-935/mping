package main

// Characterization tests for run()'s shutdown order, written before the
// supervisor / reloadCoordinator extraction so later refactors cannot silently
// reorder or drop a step:
//   trace goroutine join -> mtr stop -> pinger stop/wait/close -> port/http
//   stop/wait -> watcher stop

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

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

// TestRunStop_JoinsTracerouteBeforeStoppingPinger verifies that OnStop
// (stopAll -> tearDownAll) actually waits for the background runTraceroutes
// goroutine to exit rather than just cancelling its context, and only then
// stops the pinger. The onStop hook makes the ordering check deterministic
// (not timing-based): tearDownAll() waits on <-s.traceDone before calling
// s.p.Stop(), so a violation can only be observed if a future refactor calls
// Stop() without first joining the traceroute goroutine.
func TestRunStop_JoinsTracerouteBeforeStoppingPinger(t *testing.T) {
	started, returned := make(chan struct{}), make(chan struct{})
	fp := &fakePinger{blockTraceUntilCtxDone: true, traceRouteStarted: started, traceRouteReturned: returned}
	stoppedBeforeJoin := false
	fp.onStop = func() { stoppedBeforeJoin = stoppedBeforeJoin || !closed(returned) }
	stubRunSeams(t, func(opts ui.RunOptions) error {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Error("traceroute did not start")
			return nil
		}
		opts.OnStop()
		if !closed(returned) {
			t.Error("OnStop() returned before the in-flight traceroute goroutine exited")
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
		t.Error("pinger.Stop() was called before the traceroute goroutine had joined (shutdown order violation)")
	}
}

// TestRunStop_ClosesPingerAfterWait verifies the F1 fix end-to-end: OnStop
// (-> stopAll -> tearDownAll) must call Close() on the pinger, and only after
// Wait() has already returned (Stop -> Wait -> Close). Before the fix, the
// stop path never called Close() at all. The onClose hook makes the ordering
// check deterministic: it fires before Close() is recorded.
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
		t.Error("expected Stop() and Close() to be called — Close() is the F1 fix")
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
