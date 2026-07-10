package main

// TD-25: port checks are parsed once at startup and never re-applied on a
// YAML reload (see checkPortReloadDrift in lifecycle.go). These tests cover
// the low-risk "warn instead of silently ignoring" fix: a reload that
// changes the effective port: list should surface a restart-required
// message instead of leaving the user unaware their change had no effect.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

func TestCheckPortReloadDrift(t *testing.T) {
	tests := []struct {
		name       string
		active     []string
		reloaded   []string
		wantWarned bool
	}{
		{"identical", []string{"80/tcp"}, []string{"80/tcp"}, false},
		{"both empty", nil, nil, false},
		{"changed", []string{"80/tcp"}, []string{"443/tcp"}, true},
		{"added", nil, []string{"80/tcp"}, true},
		{"removed", []string{"80/tcp"}, nil, true},
		{"reordered", []string{"80/tcp", "443/tcp"}, []string{"443/tcp", "80/tcp"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkPortReloadDrift(tc.active, tc.reloaded)
			if (got != "") != tc.wantWarned {
				t.Errorf("checkPortReloadDrift(%v, %v) = %q, want warned=%v", tc.active, tc.reloaded, got, tc.wantWarned)
			}
			if tc.wantWarned && !strings.Contains(got, "restart") {
				t.Errorf("expected warning to mention restart, got %q", got)
			}
		})
	}
}

// TestRunReload_PortChangeWarnsRestartRequired verifies that reloading a
// hosts file whose port: list differs from what the running port checker
// was built with surfaces a warning in the next iteration's InitialLogs,
// rather than silently doing nothing.
func TestRunReload_PortChangeWarnsRestartRequired(t *testing.T) {
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
	if err := os.WriteFile(yamlPath, []byte("hosts:\n  - example.com\nport:\n  - \"80\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var sawWarning bool
	callCount := 0
	uiRun = func(opts ui.RunOptions) error {
		callCount++
		if callCount == 1 {
			time.Sleep(100 * time.Millisecond)
			if err := os.WriteFile(yamlPath,
				[]byte("hosts:\n  - example.com\nport:\n  - \"443\"\n"), 0644); err != nil {
				t.Errorf("write yaml: %v", err)
			}
			time.Sleep(600 * time.Millisecond)
			return nil
		}
		for _, line := range opts.InitialLogs {
			if strings.Contains(line, "port: change detected") {
				sawWarning = true
			}
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-f", yamlPath, "-S", "127.0.0.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
	if callCount < 2 {
		t.Fatalf("expected uiRun to be called >=2 times (reload), got %d", callCount)
	}
	if !sawWarning {
		t.Error("expected a port-change-requires-restart warning in the second iteration's InitialLogs")
	}
}
