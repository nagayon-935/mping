package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/report"
)

// recordAsRealUser replaces asRealUser with a recorder that counts calls and
// runs fn, so tests can prove each file operation goes through it.
func recordAsRealUser(t *testing.T) *int {
	t.Helper()
	orig := asRealUser
	t.Cleanup(func() { asRealUser = orig })
	calls := new(int)
	asRealUser = func(fn func() error) error {
		*calls++
		return fn()
	}
	return calls
}

func TestFileOperationsRunAsRealUser(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "list.txt", "192.0.2.1\n")
	hostsPath := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\ninclude: list.txt\n")

	tests := []struct {
		name      string
		run       func() error
		wantCalls int
	}{
		{name: "hosts file and its include", wantCalls: 2, run: func() error {
			_, err := parseHostsFile(hostsPath)
			return err
		}},
		{name: "include file", wantCalls: 1, run: func() error {
			_, err := readIncludeFile(filepath.Join(dir, "list.txt"))
			return err
		}},
		{name: "watch path discovery", wantCalls: 1, run: func() error {
			hostsFileWatchPaths(hostsPath)
			return nil
		}},
		{name: "CSV log", wantCalls: 1, run: func() error {
			f, err := setupLogger(filepath.Join(dir, "log.csv"))
			if err == nil {
				f.Close()
			}
			return err
		}},
		{name: "JSON snapshot", wantCalls: 1, run: func() error {
			return writeJSONSnapshot(filepath.Join(dir, "snap.json"), nil, nil)
		}},
		{name: "report", wantCalls: 1, run: func() error {
			return writeReportFile(filepath.Join(dir, "report.json"), "json", report.Report{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := recordAsRealUser(t)

			err := tt.run()

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *calls != tt.wantCalls {
				t.Fatalf("asRealUser called %d times, want %d", *calls, tt.wantCalls)
			}
		})
	}
}

func TestFileOperationsFailWhenPrivilegesCannotBeDropped(t *testing.T) {
	orig := asRealUser
	t.Cleanup(func() { asRealUser = orig })
	asRealUser = func(func() error) error { return errors.New("drop privileges: operation not permitted") }
	dir := t.TempDir()
	hostsPath := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\n")

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "hosts file", run: func() error { _, err := parseHostsFile(hostsPath); return err }},
		{name: "CSV log", run: func() error { _, err := setupLogger(filepath.Join(dir, "log.csv")); return err }},
		{name: "JSON snapshot", run: func() error { return writeJSONSnapshot(filepath.Join(dir, "s.json"), nil, nil) }},
		{name: "report", run: func() error {
			return writeReportFile(filepath.Join(dir, "r.json"), "json", report.Report{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()

			if err == nil || !strings.Contains(err.Error(), "drop privileges") {
				t.Fatalf("error = %v, want the drop-privileges failure", err)
			}
		})
	}
}
