package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseHostsFile_ExpandsPatternsAndKeepsNames(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "hosts.yaml", `hosts:
  - 10.0.0.1-2
  - {host: gw.example.com, name: gateway}
groups:
  - name: Core
    hosts:
      - {host: "sw{1..2}.lab", dscp: EF}
`)

	doc, err := parseHostsFile(path)

	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	wantHosts := []hostEntry{{Host: "10.0.0.1"}, {Host: "10.0.0.2"}, {Host: "gw.example.com", Name: "gateway"}}
	if !reflect.DeepEqual(doc.Hosts, wantHosts) {
		t.Fatalf("hosts = %v, want %v", doc.Hosts, wantHosts)
	}
	wantGroup := []hostEntry{{Host: "sw1.lab", DSCP: "EF"}, {Host: "sw2.lab", DSCP: "EF"}}
	if len(doc.Groups) != 1 || !reflect.DeepEqual(doc.Groups[0].Hosts, wantGroup) {
		t.Fatalf("groups = %v, want one group with %v", doc.Groups, wantGroup)
	}
}

func TestParseHostsFile_Include(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "top.txt", "192.0.2.1\n")
	writeTestFile(t, dir, "branch-a.csv", "host,name\n198.51.100.1,branch-a\n")
	writeTestFile(t, dir, "branch-b.csv", "198.51.100.2,branch-b\n")
	path := writeTestFile(t, dir, "hosts.yaml", `hosts:
  - 8.8.8.8
include: top.txt
groups:
  - name: Branches
    include: [branch-a.csv, branch-b.csv]
  - name: Mixed
    hosts: [1.1.1.1]
    include: top.txt
`)

	doc, err := parseHostsFile(path)

	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if want := []hostEntry{{Host: "8.8.8.8"}, {Host: "192.0.2.1"}}; !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("hosts = %v, want %v (inline entries first, then includes)", doc.Hosts, want)
	}
	wantGroups := []groupYAML{
		{Name: "Branches", Hosts: []hostEntry{{Host: "198.51.100.1", Name: "branch-a"}, {Host: "198.51.100.2", Name: "branch-b"}}},
		{Name: "Mixed", Hosts: []hostEntry{{Host: "1.1.1.1"}, {Host: "192.0.2.1"}}},
	}
	if !reflect.DeepEqual(doc.Groups, wantGroups) {
		t.Fatalf("groups = %#v, want %#v", doc.Groups, wantGroups)
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Fatalf("an include-only group must pass validation: %v", err)
	}
}

func TestParseHostsFile_IncludeAbsolutePath(t *testing.T) {
	incDir := t.TempDir()
	inc := writeTestFile(t, incDir, "list.txt", "192.0.2.9\n")
	path := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: "+inc+"\n")

	doc, err := parseHostsFile(path)

	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if want := []hostEntry{{Host: "192.0.2.9"}}; !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("hosts = %v, want %v", doc.Hosts, want)
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

			_, err := parseHostsFile(path)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateHostsDoc_Names(t *testing.T) {
	tests := []struct {
		name    string
		doc     hostsFileYAML
		wantErr string
	}{
		{
			name: "distinct names are valid",
			doc: hostsFileYAML{
				Hosts:  []hostEntry{{Host: "10.0.0.1", Name: "a"}, {Host: "10.0.0.1", Name: "b", DSCP: "EF"}},
				Groups: []groupYAML{{Name: "G", Hosts: []hostEntry{{Host: "10.0.0.2", Name: "c"}}}},
			},
		},
		{
			name: "duplicate name across groups",
			doc: hostsFileYAML{
				Hosts:  []hostEntry{{Host: "10.0.0.1", Name: "core"}},
				Groups: []groupYAML{{Name: "G", Hosts: []hostEntry{{Host: "10.0.0.2", Name: "core"}}}},
			},
			wantErr: `duplicate name "core"`,
		},
		{
			name: "name equal to another entry's host",
			doc: hostsFileYAML{
				Hosts: []hostEntry{{Host: "10.0.0.1", Name: "10.0.0.2"}, {Host: "10.0.0.2"}},
			},
			wantErr: `name "10.0.0.2" is also used as a host`,
		},
		{
			name: "name equal to its own host is allowed",
			doc: hostsFileYAML{
				Hosts: []hostEntry{{Host: "gw", Name: "gw"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHostsDoc(tt.doc)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
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

	got := hostsFileWatchPaths(path)

	want := []string{path, filepath.Join(dir, "top.txt"), filepath.Join(dir, "a.csv"), filepath.Join(dir, "sub", "b.csv")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestHostsFileWatchPaths_UnparsableFileWatchesOnlyItself(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: [unterminated\n")

	got := hostsFileWatchPaths(path)

	if want := []string{path}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBuildHostsAndGroups_CarriesNames(t *testing.T) {
	specs, _ := buildHostsAndGroups(
		[]hostEntry{{Host: "10.0.0.1", Name: "core"}},
		[]groupYAML{{Name: "G", Hosts: []hostEntry{{Host: "10.0.0.2", Name: "edge", DSCP: "EF"}}}},
		[]string{"cli.example"},
	)

	want := []targetSpec{
		{Host: "10.0.0.1", Name: "core"},
		{Host: "cli.example"},
		{Host: "10.0.0.2", Name: "edge", DSCP: "EF"},
	}
	if !reflect.DeepEqual(specs, want) {
		t.Fatalf("specs = %#v, want %#v", specs, want)
	}
}
