package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/ui"
)

// rewriteHostsUntil keeps rewriting the watched hosts file with content until
// done reports true, and reports whether it did within a few seconds.
//
// The file watcher registers asynchronously after run() starts, so a write
// issued right as the first UI iteration begins can be missed; repeating it
// makes the test independent of that startup race instead of sleeping for it.
// The pause between rewrites outlasts the watcher's 200ms debounce, otherwise
// each rewrite would keep resetting the debounce timer and nothing would ever
// fire. A trailing comment makes every rewrite a real modification while
// leaving the document's meaning unchanged.
func rewriteHostsUntil(t *testing.T, path, content string, done func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for n := 0; time.Now().Before(deadline); n++ {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("%s# rewrite %d\n", content, n)), 0o644); err != nil {
			t.Errorf("write hosts file: %v", err)
			return false
		}
		for end := time.Now().Add(350 * time.Millisecond); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if done() {
				return true
			}
		}
	}
	return false
}

func TestRunYAMLReload(t *testing.T) {
	stubRunSeams(t, nil)
	yamlPath := writeHostsYAML(t, "hosts:\n  - example.com\n")

	var targetCounts []int
	uiRun = func(opts ui.RunOptions) error {
		targetCounts = append(targetCounts, len(opts.Targets))
		if len(targetCounts) == 1 {
			// Edit the hosts file while the TUI is "running"; the watcher
			// should detect it and close ExternalCloseCh.
			if !rewriteHostsUntil(t, yamlPath, "hosts:\n  - example.com\n  - 1.1.1.1\n", func() bool { return closed(opts.ExternalCloseCh) }) {
				t.Error("the hosts-file change did not close ExternalCloseCh")
			}
		}
		// Return nil (simulates user quit or ExternalCloseCh-driven stop).
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-f", yamlPath, "-S", "127.0.0.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	// uiRun should have been called twice: once for the initial hosts, once
	// after the reload picked up the added host.
	if len(targetCounts) < 2 || targetCounts[0] != 1 || targetCounts[len(targetCounts)-1] != 2 {
		t.Errorf("targets per uiRun call = %v, want it to start at 1 and end at 2 (reload applied)", targetCounts)
	}
}

// TestRunReload_InvalidYAMLDoesNotTriggerReload locks in the reload
// validation gate: onFileChange must reject a document that fails
// validateHostsDoc, log the error, and leave the running instance alone
// (no restart, no uiRun re-entry).
func TestRunReload_InvalidYAMLDoesNotTriggerReload(t *testing.T) {
	stubRunSeams(t, nil)
	yamlPath := writeHostsYAML(t, "hosts:\n  - example.com\n")

	var loggedErr string
	callCount := 0
	uiRun = func(opts ui.RunOptions) error {
		callCount++
		gotLog := func() bool {
			select {
			case loggedErr = <-opts.ExternalLogCh:
				return true
			default:
				return false
			}
		}
		if !rewriteHostsUntil(t, yamlPath, "hosts: []\n", gotLog) {
			t.Error("no reload error was logged for the invalid hosts file")
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
// rather than silently doing nothing (port checks are parsed once at startup
// and never re-applied on a reload; see checkPortReloadDrift in lifecycle.go).
func TestRunReload_PortChangeWarnsRestartRequired(t *testing.T) {
	stubRunSeams(t, nil)
	yamlPath := writeHostsYAML(t, "hosts:\n  - example.com\nport:\n  - \"80\"\n")

	var sawWarning bool
	callCount := 0
	uiRun = func(opts ui.RunOptions) error {
		callCount++
		if callCount == 1 {
			if !rewriteHostsUntil(t, yamlPath, "hosts:\n  - example.com\nport:\n  - \"443\"\n", func() bool { return closed(opts.ExternalCloseCh) }) {
				t.Error("the hosts-file change did not close ExternalCloseCh")
			}
			return nil
		}
		sawWarning = containsSubstring(opts.InitialLogs, "port: change detected")
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
