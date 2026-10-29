package main

import (
	"errors"
	"os"
	"path/filepath"
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
	tests := []struct {
		name string
		run  func() error
	}{
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
// callback skipped, nothing may be written. Hosts and include files are
// covered by internal/hostsfile's own tests.
func TestFileOperationsDoAllIOInsideAsRealUser(t *testing.T) {
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

	tests := []struct {
		name string
		run  func() error
	}{
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
