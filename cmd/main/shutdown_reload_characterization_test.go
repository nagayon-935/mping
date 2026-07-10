package main

// TD-22 stage ①: characterization tests for run()'s shutdown order and
// reload sequence, written *before* any structural refactor (supervisor /
// reloadCoordinator extraction). These pin down the invariants called out in
// docs/refactoring_master_plan.md §5 risk table:
//   - trace goroutine join → mtr stop → pinger stop/wait → port/http stop/wait → watcher stop
//   - a YAML reload is only applied after validateHostsDoc succeeds
// so later stages can move this logic into new types without silently
// reordering or dropping a step.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

// orderTrackingPinger wraps fakePinger and records whether Stop() was ever
// invoked before the blocking TraceRoute call had returned. Because
// stopPinger() waits on <-curTraceDone before calling cur.Stop(), this is a
// deterministic ordering check (not timing-based): a violation can only be
// observed if a future refactor calls Stop() without first joining the
// traceroute goroutine.
type orderTrackingPinger struct {
	*fakePinger
	stopViolation bool
}

func (o *orderTrackingPinger) Stop() {
	select {
	case <-o.traceRouteReturned:
		// ok: the traceroute goroutine had already joined.
	default:
		o.stopViolation = true
	}
	o.fakePinger.Stop()
}

func TestRunStopOrder_TraceJoinsBeforePingerStop(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	returned := make(chan struct{})
	base := &fakePinger{blockTraceUntilCtxDone: true, traceRouteReturned: returned}
	tracker := &orderTrackingPinger{fakePinger: base}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return tracker
	}
	uiRun = func(opts ui.RunOptions) error {
		opts.OnStop()
		if tracker.stopViolation {
			t.Error("pinger.Stop() was called before the traceroute goroutine had joined (shutdown order violation)")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunStop_NoGoroutineLeak exercises trace + port + http together and
// verifies run() doesn't leave any of their goroutines behind once it
// returns. This is the aggregate regression guard for the "shutdown race /
// goroutine leak" risk flagged for run()'s decomposition.
func TestRunStop_NoGoroutineLeak(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := &fakePinger{}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}
	uiRun = func(opts ui.RunOptions) error {
		opts.OnStop()
		return nil
	}

	before := runtime.NumGoroutine()

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-p", "80/tcp", "-H", "http://127.0.0.1:1", "-S", "127.0.0.1", "127.0.0.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before {
		t.Errorf("goroutine leak after run() returned: before=%d after=%d", before, after)
	}
}

// TestRunReload_InvalidYAMLDoesNotTriggerReload locks in the reload
// validation gate: onFileChange must reject a document that fails
// validateHostsDoc, log the error, and leave the running instance alone
// (no restart, no uiRun re-entry).
func TestRunReload_InvalidYAMLDoesNotTriggerReload(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := &fakePinger{}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(yamlPath, []byte("hosts:\n  - example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var loggedErr string
	callCount := 0
	uiRun = func(opts ui.RunOptions) error {
		callCount++
		// Give the watcher goroutine time to register with fsnotify before
		// the write below, mirroring TestRunYAMLReload's synchronization.
		time.Sleep(100 * time.Millisecond)
		if err := os.WriteFile(yamlPath, []byte("hosts: []\n"), 0644); err != nil {
			t.Errorf("write yaml: %v", err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && loggedErr == "" {
			select {
			case msg := <-opts.ExternalLogCh:
				loggedErr = msg
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-f", yamlPath, "-S", "127.0.0.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
	if callCount != 1 {
		t.Errorf("invalid YAML must not trigger a reload: expected uiRun called once, got %d", callCount)
	}
	if !strings.Contains(loggedErr, "Reload validation error") {
		t.Errorf("expected a reload validation error to be logged, got %q", loggedErr)
	}
}
