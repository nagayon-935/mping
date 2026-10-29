package hostsfile

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
			name:    "whole-file limit spans includes and inline hosts",
			yaml:    "hosts: [10.0.0.0/22, 10.0.4.0/22, 10.0.8.0/22]\ninclude: more.txt\n",
			files:   map[string]string{"more.txt": "10.0.12.0/22\n10.0.16.0/22\n"},
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
	inc := writeTestFile(t, incDir, "list.txt", "192.0.2.9\n")
	path := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: "+inc+"\n")

	doc, err := Parse(path)

	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []Entry{{Host: "192.0.2.9"}}; !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("hosts = %v, want %v", doc.Hosts, want)
	}
}
