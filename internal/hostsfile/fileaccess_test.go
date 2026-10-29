package hostsfile

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// swapAsRealUser replaces asRealUser for one test.
func swapAsRealUser(t *testing.T, fn func(func() error) error) {
	t.Helper()
	orig := asRealUser
	t.Cleanup(func() { asRealUser = orig })
	asRealUser = fn
}

// skipAsRealUser swaps in a stand-in that reports success without running
// fn. Any file access performed outside an asRealUser callback — i.e. with
// root's permissions under a setuid install — then still happens and is
// observable, which these tests treat as a failure.
func skipAsRealUser(t *testing.T) {
	t.Helper()
	swapAsRealUser(t, func(func() error) error { return nil })
}

func TestFileAccessCallsAsRealUser(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "list.txt", "192.0.2.1\n")
	hostsPath := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\ninclude: list.txt\n")
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "hosts file", run: func() error { _, err := Parse(hostsPath); return err }},
		{name: "include file", run: func() error { _, err := readIncludeFile(filepath.Join(dir, "list.txt")); return err }},
		{name: "watch path discovery", run: func() error { WatchPaths(hostsPath); return nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			swapAsRealUser(t, func(fn func() error) error { calls++; return fn() })

			if err := tt.run(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if calls == 0 {
				t.Fatal("asRealUser was not called")
			}
		})
	}
}

// TestFileAccessHappensInsideAsRealUser proves the I/O itself — not just a
// call to asRealUser — happens inside the callback: with the callback
// skipped, nothing may be read.
func TestFileAccessHappensInsideAsRealUser(t *testing.T) {
	t.Run("hosts file is not read", func(t *testing.T) {
		path := writeTestFile(t, t.TempDir(), "hosts.yaml", "hosts: [192.0.2.9]\n")
		skipAsRealUser(t)

		doc, err := Parse(path)

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

		if _, err := Parse(missing + ".yaml"); err != nil {
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

		got := WatchPaths(path)

		if want := []string{path}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

func TestFileAccessFailsWhenPrivilegesCannotBeDropped(t *testing.T) {
	swapAsRealUser(t, func(func() error) error { return errors.New("drop privileges: operation not permitted") })
	dir := t.TempDir()
	hostsPath := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\n")
	incPath := writeTestFile(t, dir, "list.txt", "192.0.2.1\n")

	for name, run := range map[string]func() error{
		"hosts file":   func() error { _, err := Parse(hostsPath); return err },
		"include file": func() error { _, err := readIncludeFile(incPath); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil || !strings.Contains(err.Error(), "drop privileges") {
				t.Fatalf("error = %v, want the drop-privileges failure", err)
			}
		})
	}
	t.Run("watch path discovery falls back to the hosts file", func(t *testing.T) {
		// The include would be listed if the read had succeeded.
		withInclude := writeTestFile(t, dir, "with-include.yaml", "include: a.csv\n")

		got := WatchPaths(withInclude)

		if want := []string{withInclude}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}
