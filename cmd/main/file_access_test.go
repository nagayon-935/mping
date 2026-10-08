package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/report"
	"github.com/nagayon-935/mping/internal/stats"
)

// swapAsRealUser replaces asRealUser for one test.
func swapAsRealUser(t *testing.T, fn func(func() error) error) {
	t.Helper()
	orig := asRealUser
	t.Cleanup(func() { asRealUser = orig })
	asRealUser = fn
}

// recordAsRealUser swaps in a recorder that counts calls and runs fn.
func recordAsRealUser(t *testing.T) *int {
	t.Helper()
	calls := new(int)
	swapAsRealUser(t, func(fn func() error) error {
		*calls++
		return fn()
	})
	return calls
}

// skipAsRealUser swaps in a stand-in that reports success without running
// fn. Any file access an operation performs outside its asRealUser
// callback — i.e. with root's permissions under a setuid install — then
// still happens and is observable, which these tests treat as a failure.
func skipAsRealUser(t *testing.T) {
	t.Helper()
	swapAsRealUser(t, func(func() error) error { return nil })
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s was accessed outside asRealUser (stat err = %v)", filepath.Base(path), err)
	}
}

func TestFileOperationsCallAsRealUser(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "list.txt", "192.0.2.1\n")
	hostsPath := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\ninclude: list.txt\n")
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "hosts file", run: func() error { _, err := parseHostsFile(hostsPath); return err }},
		{name: "include file", run: func() error { _, err := readIncludeFile(filepath.Join(dir, "list.txt")); return err }},
		{name: "watch path discovery", run: func() error { hostsFileWatchPaths(hostsPath); return nil }},
		{name: "CSV log", run: func() error {
			f, err := setupLogger(filepath.Join(dir, "log.csv"))
			if err == nil {
				f.Close()
			}
			return err
		}},
		{name: "JSON snapshot", run: func() error { return writeJSONSnapshot(filepath.Join(dir, "snap.json"), nil, nil) }},
		{name: "report", run: func() error {
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
			if *calls == 0 {
				t.Fatal("asRealUser was not called")
			}
		})
	}
}

// TestFileOperationsDoAllIOInsideAsRealUser proves the I/O itself — not
// just a call to asRealUser — happens inside the callback: with the
// callback skipped, nothing may be read or written.
func TestFileOperationsDoAllIOInsideAsRealUser(t *testing.T) {
	t.Run("hosts file is not read", func(t *testing.T) {
		dir := t.TempDir()
		path := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\n")
		skipAsRealUser(t)

		doc, err := parseHostsFile(path)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(doc.Hosts) != 0 {
			t.Fatalf("hosts file was read outside asRealUser: %v", doc.Hosts)
		}
	})
	t.Run("missing files are not even looked up", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		skipAsRealUser(t)

		if _, err := parseHostsFile(missing + ".yaml"); err != nil {
			t.Fatalf("hosts file was looked up outside asRealUser: %v", err)
		}
		if _, err := readIncludeFile(missing + ".txt"); err != nil {
			t.Fatalf("include file was looked up outside asRealUser: %v", err)
		}
	})
	t.Run("include file is not read", func(t *testing.T) {
		path := writeTestFile(t, t.TempDir(), "list.txt", "192.0.2.1\n")
		skipAsRealUser(t)

		got, err := readIncludeFile(path)

		if err != nil || len(got) != 0 {
			t.Fatalf("include file was read outside asRealUser: %v, %v", got, err)
		}
	})
	t.Run("watch paths come only from a read done as the user", func(t *testing.T) {
		path := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: a.csv\n")
		skipAsRealUser(t)

		got := hostsFileWatchPaths(path)

		if want := []string{path}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("CSV log is not created", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "log.csv")
		skipAsRealUser(t)

		if f, _ := setupLogger(path); f != nil {
			f.Close()
		}

		assertNotExist(t, path)
	})
	t.Run("JSON snapshot is not written", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "snap.json")
		skipAsRealUser(t)

		if err := writeJSONSnapshot(path, nil, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertNotExist(t, path)
		assertNotExist(t, path+".tmp")
	})
	t.Run("report is not written", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.json")
		skipAsRealUser(t)

		if err := writeReportFile(path, "json", report.Report{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertNotExist(t, path)
	})
}

func TestSupervisorSaveReportWritesAsRealUser(t *testing.T) {
	s := newSupervisor(supervisorConfig{
		targets: []*stats.TargetStats{stats.NewTargetStats("base")},
		specs:   []targetSpec{{Host: "base"}},
	})
	s.Start()
	t.Cleanup(s.Shutdown)
	path := filepath.Join(t.TempDir(), "report.json")
	skipAsRealUser(t)

	if err := s.saveReport(path, "json", 0); err != nil {
		t.Fatalf("saveReport: %v", err)
	}

	assertNotExist(t, path)
}

func TestSupervisorSaveReportResolvesPathAsRealUser(t *testing.T) {
	dir := t.TempDir()
	reserved := filepath.Join(dir, "live.json")
	s := newSupervisor(supervisorConfig{
		targets:         []*stats.TargetStats{stats.NewTargetStats("base")},
		specs:           []targetSpec{{Host: "base"}},
		reservedOutputs: []string{reserved},
	})
	s.Start()
	t.Cleanup(s.Shutdown)
	calls := recordAsRealUser(t)

	err := s.saveReport(reserved, "json", 0)

	if err == nil {
		t.Fatal("reserved path accepted")
	}
	// Rejected before any write: the only asRealUser call is the symlink
	// resolution of the requested path, which would otherwise probe the
	// filesystem with root's permissions.
	if *calls == 0 {
		t.Fatal("report path was resolved outside asRealUser")
	}
}

func TestFileOperationsFailWhenPrivilegesCannotBeDropped(t *testing.T) {
	swapAsRealUser(t, func(func() error) error { return errors.New("drop privileges: operation not permitted") })
	dir := t.TempDir()
	hostsPath := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\n")
	incPath := writeTestFile(t, dir, "list.txt", "192.0.2.1\n")

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "hosts file", run: func() error { _, err := parseHostsFile(hostsPath); return err }},
		{name: "include file", run: func() error { _, err := readIncludeFile(incPath); return err }},
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
	t.Run("watch path discovery falls back to the hosts file", func(t *testing.T) {
		// The include would be listed if the read had succeeded.
		withInclude := writeTestFile(t, dir, "with-include.yaml", "include: a.csv\n")

		got := hostsFileWatchPaths(withInclude)

		if want := []string{withInclude}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}
