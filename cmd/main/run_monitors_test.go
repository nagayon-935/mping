package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/ui"
)

// TestRunRouteModes verifies that -T and -M both run the route engine and
// wire its reset into the UI. The fake pinger's prober cannot open a hop
// socket, so a running engine records that failure on the target.
func TestRunRouteModes(t *testing.T) {
	for _, tt := range []struct {
		flag string
		want ui.RouteMode
	}{{"-T", ui.RoutePath}, {"-M", ui.RouteMonitor}} {
		t.Run(tt.flag, func(t *testing.T) {
			stubRunSeams(t, func(opts ui.RunOptions) error {
				if opts.Route != tt.want {
					t.Errorf("Route = %v, want %v", opts.Route, tt.want)
				}
				if opts.OnResetRoute == nil {
					t.Error("OnResetRoute must not be nil when the route pane is shown")
					return nil
				}
				routeFailed := func() bool { return opts.Targets[0].GetView().MTRError != "" }
				if !pollUntil(2*time.Second, routeFailed) {
					t.Error("route engine did not run")
				}
				opts.OnResetRoute()
				if !pollUntil(2*time.Second, routeFailed) {
					t.Error("route engine did not run again after OnResetRoute()")
				}
				return nil
			})

			var out, errOut bytes.Buffer
			if code := run([]string{tt.flag, "-S", "127.0.0.1", "example.com"}, &out, &errOut); code != 0 {
				t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
			}
		})
	}
}

func TestRunRejectsBothRouteModes(t *testing.T) {
	stubRunSeams(t, func(ui.RunOptions) error {
		t.Error("the UI started with both -T and -M")
		return nil
	})
	var out, errOut bytes.Buffer
	if code := run([]string{"-T", "-M", "-S", "127.0.0.1", "example.com"}, &out, &errOut); code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if got := errOut.String(); !strings.HasPrefix(got, "Error: ") || !strings.Contains(got, "cannot be used together") {
		t.Fatalf("stderr = %q, want the -T/-M conflict without a hosts-file prefix", got)
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
