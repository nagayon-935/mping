package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

func TestRunHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--help"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if !strings.Contains(out.String(), "Usage: mping") {
		t.Fatalf("expected usage in output")
	}
}

func TestRunMissingHosts(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
	if !strings.Contains(errOut.String(), "Usage: mping") {
		t.Fatalf("expected usage in error output")
	}
}

func TestRunInvalidIPv4IPv6Flags(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"-4", "-6", "example.com"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
}

func TestRunMissingHostsFile(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"-f", "does-not-exist.yaml"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
	if !strings.Contains(errOut.String(), "Error reading hosts file") {
		t.Fatalf("expected hosts file error")
	}
}

func TestRunStopRestart(t *testing.T) {
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
		if err := opts.OnRestart(); err != nil {
			t.Fatalf("restart failed: %v", err)
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if !fp.isStarted() || !fp.isReleased() || !fp.Waited() {
		t.Fatalf("expected pinger lifecycle, started=%v released=%v waited=%v", fp.isStarted(), fp.isReleased(), fp.Waited())
	}
}

// TestRunStop_JoinsTracerouteGoroutine verifies that OnStop (stopAll ->
// tearDownAll) actually waits for the background runTraceroutes goroutine to
// exit rather than just cancelling its context and returning immediately.
func TestRunStop_JoinsTracerouteGoroutine(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	returned := make(chan struct{})
	started := make(chan struct{})
	fp := &fakePinger{blockTraceUntilCtxDone: true, traceRouteStarted: started, traceRouteReturned: returned}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}
	uiRun = func(opts ui.RunOptions) error {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("traceroute did not start")
		}
		opts.OnStop()
		select {
		case <-returned:
		default:
			t.Error("OnStop() returned before the in-flight traceroute goroutine exited")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

func TestRunStartError(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := &fakePinger{startErr: io.ErrUnexpectedEOF}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}
	uiRun = func(opts ui.RunOptions) error {
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
	if !strings.Contains(errOut.String(), "Error starting pinger") {
		t.Fatalf("expected start error output")
	}
}

// ---- pingerAdapter setter tests ----

// ---- makePingerFactory interface wiring tests ----

// ---- runTraceroutes tests ----

// ---- determineSourceIPs additional tests ----

// ---- getInterfaceIP additional tests ----

// ---- run() additional coverage ----

func TestRunInvalidPortSpec(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return &fakePinger{}
	}
	uiRun = func(opts ui.RunOptions) error {
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-p", "notaport", "example.com"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code for invalid port spec")
	}
	if !strings.Contains(strings.ToLower(errOut.String()), "invalid port spec") {
		t.Errorf("expected invalid port spec message, got: %q", errOut.String())
	}
}

func TestRunWithPortSpec(t *testing.T) {
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
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-p", "443/tcp", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

func TestRunWithTrace(t *testing.T) {
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
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunResetTrace verifies that the onResetTrace callback passed to uiRun,
// when called, re-triggers a traceroute and repopulates TraceHops.
func TestRunResetTrace(t *testing.T) {
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
		if opts.OnResetTrace == nil {
			t.Error("OnResetTrace must not be nil when TraceEnabled=true")
			return nil
		}
		// Simulate the UI clearing TraceHops before calling OnResetTrace (as 'R' does).
		for _, tg := range opts.Targets {
			tg.SetTraceHops(nil)
		}
		opts.OnResetTrace()
		// Wait for the re-run to populate TraceHops.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(opts.Targets[0].GetView().TraceHops) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if hops := opts.Targets[0].GetView().TraceHops; len(hops) == 0 {
			t.Error("TraceHops not repopulated after OnResetTrace()")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunResetPort verifies that the onResetPort callback passed to uiRun is non-nil when port specs are provided,
// and that calling it stops and restarts the port checker.
func TestRunResetPort(t *testing.T) {
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
		if !opts.PortEnabled {
			t.Error("PortEnabled should be true when port specs given")
			return nil
		}
		if opts.OnResetPort == nil {
			t.Error("OnResetPort must not be nil when port specs are provided")
			return nil
		}
		// Calling it must not panic
		opts.OnResetPort()
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-p", "80/tcp", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

func TestRunWithHTTP(t *testing.T) {
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
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-H", "http://127.0.0.1:1", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunResetHTTP verifies that the onResetHTTP callback passed to uiRun is non-nil when
// HTTP URLs are provided, and that calling it stops (joining the checker's goroutines via
// Wait, per the fix in httpchecker.go) and swaps in a fresh HTTPChecker whose results are
// reachable through the live HTTPResults accessor.
func TestRunResetHTTP(t *testing.T) {
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
		if !opts.HTTPEnabled {
			t.Error("HTTPEnabled should be true when HTTP URLs given")
			return nil
		}
		if opts.OnResetHTTP == nil {
			t.Error("OnResetHTTP must not be nil when HTTP URLs are provided")
			return nil
		}
		if opts.HTTPResults == nil {
			t.Error("HTTPResults must not be nil when HTTP URLs are provided")
			return nil
		}
		before := opts.HTTPResults()
		if len(before) != 1 {
			t.Fatalf("HTTPResults before reset: got %d entries, want 1", len(before))
		}
		// Calling it must not panic, and must join the old checker's goroutines
		// before swapping in a new one.
		opts.OnResetHTTP()
		after := opts.HTTPResults()
		if len(after) != 1 {
			t.Fatalf("HTTPResults after reset: got %d entries, want 1", len(after))
		}
		if before[0] == after[0] {
			t.Error("OnResetHTTP should swap in a new HTTPCheckResult, not reuse the old one")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-H", "http://127.0.0.1:1", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

func TestRunWithMTUIPv6Warning(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return &fakePinger{}
	}
	uiRun = func(opts ui.RunOptions) error {
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-6", "-m", "-S", "::1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "PMTU discovery disabled") {
		t.Errorf("expected PMTU warning, got: %q", errOut.String())
	}
}

// ---- pingerAdapter.Close() test ----

// ---- setupPMTU tests ----

// ---- validateHostsDoc tests ----

// ---- validateHostsDoc dscp validation ----

// ---- parseArgs -j/--json-output flag tests ----

// ---- writeJSONSnapshot tests ----

// ---- run() with -j flag ----

// ---- YAML auto-reload integration test ----

func TestRunYAMLReload(t *testing.T) {
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

	callCount := 0
	uiRun = func(opts ui.RunOptions) error {
		callCount++
		if callCount == 1 {
			// Write a new hosts file while the TUI is "running".
			// The watcher should detect this and close ExternalCloseCh.
			time.Sleep(100 * time.Millisecond)
			if err := os.WriteFile(yamlPath,
				[]byte("hosts:\n  - example.com\n  - 1.1.1.1\n"), 0644); err != nil {
				t.Errorf("write yaml: %v", err)
			}
			// Wait for watcher debounce + processing.
			time.Sleep(600 * time.Millisecond)
		}
		// Return nil (simulates user quit or ExternalCloseCh-driven stop).
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-f", yamlPath, "-S", "127.0.0.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	// uiRun should have been called twice: once for initial, once after reload.
	if callCount < 2 {
		t.Errorf("expected uiRun to be called >=2 times (reload), got %d", callCount)
	}
}

// ---- threshold configuration tests ----

// ── Phase 5: YAML groups ──────────────────────────────────────────────────────

func TestRunOnAddHost_PreservesSession(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := newLiveFakePinger()
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}

	reloadCount := 0
	uiRun = func(opts ui.RunOptions) error {
		reloadCount++
		if reloadCount == 1 {
			// First run: call OnAddHost to add a new host
			if err := opts.OnAddHost("newhost.example.com"); err != nil {
				t.Errorf("OnAddHost: unexpected error: %v", err)
			}
		}
		if got := len(opts.TargetSource().Targets); got != 2 {
			t.Errorf("live target count=%d", got)
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d; stderr: %s", code, errOut.String())
	}
	if reloadCount != 1 {
		t.Fatalf("expected one persistent UI run, got %d", reloadCount)
	}
}

func TestRunOnAddHost_DuplicateReturnsError(t *testing.T) {
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
		// Try to add the same host that already exists
		if err := opts.OnAddHost("example.com"); err == nil {
			t.Error("OnAddHost with duplicate host should return error")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestRunOnDeleteHost_PreservesSession(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := newLiveFakePinger()
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}

	reloadCount := 0
	uiRun = func(opts ui.RunOptions) error {
		reloadCount++
		if reloadCount == 1 {
			// Delete the second host (keep "host-a")
			if err := opts.OnDeleteHost("host-b"); err != nil {
				t.Errorf("OnDeleteHost: unexpected error: %v", err)
			}
		}
		if got := len(opts.TargetSource().Targets); got != 1 {
			t.Errorf("live target count=%d", got)
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "host-a", "host-b"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d; stderr: %s", code, errOut.String())
	}
	if reloadCount != 1 {
		t.Fatalf("expected one persistent UI run, got %d", reloadCount)
	}
}

func TestRunOnDeleteHost_LastHostReturnsError(t *testing.T) {
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
		// Try to delete the only host
		if err := opts.OnDeleteHost("example.com"); err == nil {
			t.Error("OnDeleteHost of last host should return error")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestRunOnDeleteHost_NotFoundReturnsError(t *testing.T) {
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
		if err := opts.OnDeleteHost("nonexistent.host"); err == nil {
			t.Error("OnDeleteHost of nonexistent host should return error")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

// ---- setupLogger additional coverage ----

// ---- writeJSONSnapshot error path ----

// ---- printExitSummary additional coverage ----

// ---- resolverBindConfig ----

// ---- exit code tests (-c/--count vs. total loss, TD: apple ping.c parity) ----

// TestRun_ExitCode drives run() end-to-end with a mocked pinger/UI, covering
// the (-c present/absent) x (total loss/partial/full response) matrix from
// the exit-code spec: only a bounded (-c) run that received zero replies on
// every target should exit exitCodeNoResponse; everything else stays 0. The
// pre-existing error paths (return 1) are untouched by this feature and are
// not re-tested here.
func TestRun_ExitCode(t *testing.T) {
	tests := []struct {
		name      string
		withCount bool
		simulate  func(targets []*stats.TargetStats)
		wantCode  int
	}{
		{
			name:      "no --count, total loss still exits 0",
			withCount: false,
			simulate: func(targets []*stats.TargetStats) {
				for _, tgt := range targets {
					tgt.IncSent()
				}
			},
			wantCode: 0,
		},
		{
			name:      "--count set, total loss exits exitCodeNoResponse",
			withCount: true,
			simulate: func(targets []*stats.TargetStats) {
				for _, tgt := range targets {
					tgt.IncSent()
				}
			},
			wantCode: exitCodeNoResponse,
		},
		{
			name:      "--count set, partial response exits 0",
			withCount: true,
			simulate: func(targets []*stats.TargetStats) {
				targets[0].IncSent()
				targets[0].OnSuccess(10*time.Millisecond, 64)
				targets[1].IncSent()
			},
			wantCode: 0,
		},
		{
			name:      "--count set, every target responded exits 0",
			withCount: true,
			simulate: func(targets []*stats.TargetStats) {
				for _, tgt := range targets {
					tgt.IncSent()
					tgt.OnSuccess(10*time.Millisecond, 64)
				}
			},
			wantCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origPinger := newPinger
			origUI := uiRun
			t.Cleanup(func() {
				newPinger = origPinger
				uiRun = origUI
			})

			var captured []*stats.TargetStats
			newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
				captured = targets
				return &fakePinger{}
			}
			uiRun = func(opts ui.RunOptions) error {
				tt.simulate(captured)
				return nil
			}

			args := []string{"-S", "127.0.0.1"}
			if tt.withCount {
				args = append(args, "-c", "3")
			}
			args = append(args, "example.com", "example.org")

			var out, errOut bytes.Buffer
			code := run(args, &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("expected %d, got %d (err: %s)", tt.wantCode, code, errOut.String())
			}
		})
	}
}

// ---- --duration tests ----

// TestRunDurationExpires verifies --duration converges onto the existing
// ExternalCloseCh shutdown path (the same one YAML reload uses): once the
// deadline elapses, uiRun's ExternalCloseCh must close, uiRun must be called
// exactly once (no reload re-entry), and printExitSummary's output must
// still appear on stdout after run() returns.
func TestRunDurationExpires(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return &fakePinger{}
	}

	callCount := 0
	uiRun = func(opts ui.RunOptions) error {
		callCount++
		select {
		case <-opts.ExternalCloseCh:
		case <-time.After(2 * time.Second):
			t.Error("--duration did not close ExternalCloseCh in time")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"--duration", "50ms", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
	if callCount != 1 {
		t.Errorf("expected exactly one uiRun call (no reload re-entry), got %d", callCount)
	}
	if !strings.Contains(out.String(), "mping statistics") {
		t.Errorf("expected exit summary in stdout, got: %q", out.String())
	}
}

// TestRunNoDuration_ExternalCloseChNotFired guards against a regression where
// watchDurationLimit would fire even when --duration is left at its disabled
// (0) default.
func TestRunNoDuration_ExternalCloseChNotFired(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return &fakePinger{}
	}

	uiRun = func(opts ui.RunOptions) error {
		select {
		case <-opts.ExternalCloseCh:
			t.Error("ExternalCloseCh fired even though --duration was not set")
		case <-time.After(100 * time.Millisecond):
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}
