package hostsfile

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseHostsFile_Mapping(t *testing.T) {
	path := writeHostsYAML(t, "hosts:\n  - example.com\n  - 8.8.8.8\n")

	got, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Entry{{Host: "example.com"}, {Host: "8.8.8.8"}}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts: got %v, want %v", got.Hosts, want)
	}
}

func TestParseHostsFile_GroupsYAML(t *testing.T) {
	path := writeHostsYAML(t, "groups:\n  - name: \"Site A\"\n    hosts:\n      - 1.1.1.1\n      - 8.8.8.8\n")

	doc, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Groups) != 1 {
		t.Fatalf("Groups: want 1, got %d", len(doc.Groups))
	}
	if doc.Groups[0].Name != "Site A" {
		t.Errorf("Groups[0].Name: want %q, got %q", "Site A", doc.Groups[0].Name)
	}
	if len(doc.Groups[0].Hosts) != 2 {
		t.Errorf("Groups[0].Hosts: want 2, got %d", len(doc.Groups[0].Hosts))
	}
}

func TestParseHostsFile_EmptyFileIsNotAnError(t *testing.T) {
	path := writeHostsYAML(t, "")

	got, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse on empty file: unexpected error: %v", err)
	}
	if len(got.Hosts) != 0 {
		t.Errorf("hosts: got %v, want empty", got.Hosts)
	}
}

// TestParseHostsFile_RejectsInvalidDocuments pins strict decoding: a typo in a
// key must be surfaced as an error rather than silently ignored.
func TestParseHostsFile_RejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"host mapping missing the required 'host' key", "hosts:\n  - name: edge\n"},
		{"unknown key in host mapping (typo of name)", "hosts:\n  - host: example.com\n    nmae: edge\n"},
		{"unknown top-level key (typo of interval)", "hosts:\n  - example.com\ninterva: 500\n"},
		{"unknown key in nested group (host for hosts)", "groups:\n  - name: web\n    host:\n      - example.com\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(writeHostsYAML(t, tt.content)); err == nil {
				t.Fatal("Parse: expected an error, got nil")
			}
		})
	}
}

func TestParseHostsFile_AmbiguousYAML(t *testing.T) {
	for _, tt := range []struct{ name, content, reason string }{
		{"second document", "hosts: [a]\n---\nhosts: [b]", "line 2: hosts file must contain a single YAML document"},
		{"empty second document", "hosts: [a]\n---\n", "line 2: hosts file must contain a single YAML document"},
		{"malformed second document", "hosts: [a]\n---\n[", "hosts file must contain a single YAML document"},
		{"duplicate host", "hosts:\n  - host: a\n    host: b", "line 3: duplicate key \"host\""},
		{"duplicate group host name", "groups:\n- name: g\n  hosts:\n  - host: a\n    name: x\n    name: y", "line 6: duplicate key \"name\""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(writeHostsYAML(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.reason) {
				t.Errorf("Parse error = %v, want %q", err, tt.reason)
			}
		})
	}
}

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
	writeTestFile(t, dir, "list.csv", netBoxCSV("192.0.2.1/24,Active,"))
	hostsPath := writeTestFile(t, dir, "hosts.yaml", "hosts: [192.0.2.9]\ninclude: list.csv\n")
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "hosts file", run: func() error { _, err := Parse(hostsPath); return err }},
		{name: "include file", run: func() error { _, err := readIncludeFile(filepath.Join(dir, "list.csv")); return err }},
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

func TestHostsFileWatchPaths(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "hosts.yaml", `include: top.txt
groups:
  - name: A
    include: [a.csv, top.txt]
  - name: B
    include: sub/b.csv
`)
	// Include files need not exist yet: watching them lets creating a
	// missing file trigger a reload.

	got := WatchPaths(path)

	want := []string{path, filepath.Join(dir, "top.txt"), filepath.Join(dir, "a.csv"), filepath.Join(dir, "sub", "b.csv")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestHostsFileWatchPaths_UnparsableFileWatchesOnlyItself(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: [unterminated\n")

	got := WatchPaths(path)

	if want := []string{path}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseHostsFile_ExpandsPatternsAndKeepsNames(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "hosts.yaml", `hosts:
  - 10.0.0.1-2
  - {host: gw.example.com, name: gateway}
groups:
  - name: Core
    hosts:
      - "sw{1..2}.lab"
`)

	doc, err := Parse(path)

	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	wantHosts := []Entry{{Host: "10.0.0.1"}, {Host: "10.0.0.2"}, {Host: "gw.example.com", Name: "gateway"}}
	if !reflect.DeepEqual(doc.Hosts, wantHosts) {
		t.Fatalf("hosts = %v, want %v", doc.Hosts, wantHosts)
	}
	wantGroup := []Entry{{Host: "sw1.lab"}, {Host: "sw2.lab"}}
	if len(doc.Groups) != 1 || !reflect.DeepEqual(doc.Groups[0].Hosts, wantGroup) {
		t.Fatalf("groups = %v, want one group with %v", doc.Groups, wantGroup)
	}
}

func TestParseHostsFile_ExpansionErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		files   map[string]string
		wantErr string
	}{
		{
			name:    "pattern error names the entry and value",
			yaml:    "groups:\n  - name: Core\n    hosts: [a, 10.0.0.0/16]\n",
			wantErr: `groups["Core"][1] "10.0.0.0/16": expands to more than 1024 hosts`,
		},
		{
			name:    "missing include file",
			yaml:    "include: nope.csv\n",
			wantErr: "nope.csv",
		},
		{
			name:    "include entry must be a string",
			yaml:    "include: {a: b}\n",
			wantErr: "include: line 1: expected a file path or a list of file paths",
		},
		{
			name:    "empty include path",
			yaml:    "hosts: [a]\ninclude: \"\"\n",
			wantErr: "include: line 2: empty file path",
		},
		{
			name:    "empty include path in a list",
			yaml:    "hosts: [a]\ninclude: [a.txt, '']\n",
			wantErr: "include: line 2: empty file path",
		},
		{
			name: "whole-file limit spans includes and inline hosts",
			yaml: "hosts: [10.0.0.0/22, 10.0.4.0/22, 10.0.8.0/22, 10.0.12.0/22]\ninclude: more.csv\n",
			// 4 x 1022 inline hosts plus 9 NetBox rows: 4097 targets.
			files:   map[string]string{"more.csv": netBoxCSV("10.0.16.1/22,Active,", "10.0.16.2/22,Active,", "10.0.16.3/22,Active,", "10.0.16.4/22,Active,", "10.0.16.5/22,Active,", "10.0.16.6/22,Active,", "10.0.16.7/22,Active,", "10.0.16.8/22,Active,", "10.0.16.9/22,Active,")},
			wantErr: "more than 4096 targets",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeTestFile(t, dir, name, content)
			}
			path := writeTestFile(t, dir, "hosts.yaml", tt.yaml)

			_, err := Parse(path)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseHostsFile_IncludeAbsolutePath(t *testing.T) {
	incDir := t.TempDir()
	inc := writeTestFile(t, incDir, "list.csv", netBoxCSV("192.0.2.9/24,Active,"))
	path := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: "+inc+"\n")

	doc, err := Parse(path)

	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []Entry{{Host: "192.0.2.9"}}; !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("hosts = %v, want %v", doc.Hosts, want)
	}
}
