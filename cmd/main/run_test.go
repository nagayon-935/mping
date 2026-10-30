package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

// TestRun_CLIErrors drives run() through the argument errors that must stop it
// before any UI or pinger starts.
func TestRun_CLIErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int // 0 means success (help); otherwise any non-zero
		inStdout string
		inStderr string
	}{
		{"--help prints usage and succeeds", []string{"--help"}, 0, "Usage: mping", ""},
		{"missing hosts prints usage", []string{}, 1, "", "Usage: mping"},
		{"-4 with -6 is rejected", []string{"-4", "-6", "example.com"}, 1, "", "cannot use both -4 and -6"},
		{"missing hosts file", []string{"-f", "does-not-exist.yaml"}, 1, "", "Error reading hosts file"},
		{"invalid port spec", []string{"-p", "notaport", "example.com"}, 1, "", "invalid port spec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubRunSeams(t, func(ui.RunOptions) error {
				t.Error("the UI must not start")
				return nil
			})

			var out, errOut bytes.Buffer
			code := run(tt.args, &out, &errOut)

			if (code == 0) != (tt.wantCode == 0) {
				t.Fatalf("exit code = %d, want %s", code, map[bool]string{true: "0", false: "non-zero"}[tt.wantCode == 0])
			}
			if !strings.Contains(out.String(), tt.inStdout) {
				t.Errorf("stdout %q lacks %q", out.String(), tt.inStdout)
			}
			if !strings.Contains(strings.ToLower(errOut.String()), strings.ToLower(tt.inStderr)) {
				t.Errorf("stderr %q lacks %q", errOut.String(), tt.inStderr)
			}
		})
	}
}

func TestRunStartError(t *testing.T) {
	stubRunSeams(t, func(ui.RunOptions) error { return nil })
	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController {
		return &fakePinger{startErr: io.ErrUnexpectedEOF}
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
	if !strings.Contains(errOut.String(), "Error starting pinger") {
		t.Fatalf("expected start error output, got %q", errOut.String())
	}
}

func TestRunWithMTUIPv6Warning(t *testing.T) {
	stubRunSeams(t, func(ui.RunOptions) error { return nil })

	var out, errOut bytes.Buffer
	code := run([]string{"-6", "-m", "-S", "::1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "PMTU discovery disabled") {
		t.Errorf("expected PMTU warning, got: %q", errOut.String())
	}
}

// TestRun_ExitCode drives run() end-to-end with a mocked pinger/UI, covering
// the (-c present/absent) x (total loss/partial/full response) matrix from
// the exit-code spec: only a bounded (-c) run that received zero replies on
// every target should exit exitCodeNoResponse; everything else stays 0. The
// error paths (return 1) are covered elsewhere.
func TestRun_ExitCode(t *testing.T) {
	sentOnly := func(targets []*stats.TargetStats) {
		for _, tgt := range targets {
			tgt.IncSent()
		}
	}
	tests := []struct {
		name      string
		withCount bool
		simulate  func(targets []*stats.TargetStats)
		wantCode  int
	}{
		{"no --count, total loss still exits 0", false, sentOnly, 0},
		{"--count set, total loss exits exitCodeNoResponse", true, sentOnly, exitCodeNoResponse},
		{
			"--count set, partial response exits 0", true,
			func(targets []*stats.TargetStats) {
				targets[0].IncSent()
				targets[0].OnSuccess(10*time.Millisecond, 64)
				targets[1].IncSent()
			}, 0,
		},
		{
			"--count set, every target responded exits 0", true,
			func(targets []*stats.TargetStats) {
				for _, tgt := range targets {
					tgt.IncSent()
					tgt.OnSuccess(10*time.Millisecond, 64)
				}
			}, 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured []*stats.TargetStats
			stubRunSeams(t, func(ui.RunOptions) error {
				tt.simulate(captured)
				return nil
			})
			newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
				captured = targets
				return &fakePinger{}
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

// TestRunDurationExpires verifies --duration converges onto the existing
// ExternalCloseCh shutdown path (the same one YAML reload uses): once the
// deadline elapses, uiRun's ExternalCloseCh must close, uiRun must be called
// exactly once (no reload re-entry), and printExitSummary's output must
// still appear on stdout after run() returns.
func TestRunDurationExpires(t *testing.T) {
	callCount := 0
	stubRunSeams(t, func(opts ui.RunOptions) error {
		callCount++
		select {
		case <-opts.ExternalCloseCh:
		case <-time.After(2 * time.Second):
			t.Error("--duration did not close ExternalCloseCh in time")
		}
		return nil
	})

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
	stubRunSeams(t, func(opts ui.RunOptions) error {
		select {
		case <-opts.ExternalCloseCh:
			t.Error("ExternalCloseCh fired even though --duration was not set")
		case <-time.After(100 * time.Millisecond):
		}
		return nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}
