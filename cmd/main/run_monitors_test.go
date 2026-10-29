package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/ui"
)

// TestRunResetTrace verifies that run -T wires OnResetTrace into the UI and
// that calling it re-triggers a traceroute and repopulates TraceHops.
func TestRunResetTrace(t *testing.T) {
	stubRunSeams(t, func(opts ui.RunOptions) error {
		if opts.OnResetTrace == nil {
			t.Error("OnResetTrace must not be nil when TraceEnabled=true")
			return nil
		}
		// Simulate the UI clearing TraceHops before calling OnResetTrace (as 'R' does).
		for _, tg := range opts.Targets {
			tg.SetTraceHops(nil)
		}
		opts.OnResetTrace()
		if !pollUntil(2*time.Second, func() bool { return len(opts.Targets[0].GetView().TraceHops) > 0 }) {
			t.Error("TraceHops not repopulated after OnResetTrace()")
		}
		return nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunResetPort verifies that the OnResetPort callback passed to uiRun is
// non-nil when port specs are provided, and that calling it stops and
// restarts the port checker without panicking.
func TestRunResetPort(t *testing.T) {
	stubRunSeams(t, func(opts ui.RunOptions) error {
		if !opts.PortEnabled {
			t.Error("PortEnabled should be true when port specs given")
			return nil
		}
		if opts.OnResetPort == nil {
			t.Error("OnResetPort must not be nil when port specs are provided")
			return nil
		}
		opts.OnResetPort()
		return nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{"-p", "80/tcp", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunResetHTTP verifies that the OnResetHTTP callback passed to uiRun is
// non-nil when HTTP URLs are provided, and that calling it stops (joining the
// checker's goroutines via Wait) and swaps in a fresh HTTPChecker whose
// results are reachable through the live HTTPResults accessor.
func TestRunResetHTTP(t *testing.T) {
	stubRunSeams(t, func(opts ui.RunOptions) error {
		if !opts.HTTPEnabled {
			t.Error("HTTPEnabled should be true when HTTP URLs given")
			return nil
		}
		if opts.OnResetHTTP == nil || opts.HTTPResults == nil {
			t.Error("OnResetHTTP and HTTPResults must not be nil when HTTP URLs are provided")
			return nil
		}
		before := opts.HTTPResults()
		if len(before) != 1 {
			t.Errorf("HTTPResults before reset: got %d entries, want 1", len(before))
			return nil
		}
		opts.OnResetHTTP()
		after := opts.HTTPResults()
		if len(after) != 1 {
			t.Errorf("HTTPResults after reset: got %d entries, want 1", len(after))
			return nil
		}
		if before[0] == after[0] {
			t.Error("OnResetHTTP should swap in a new HTTPCheckResult, not reuse the old one")
		}
		return nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{"-H", "http://127.0.0.1:1", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}
