package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/hostsfile"
)

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

	doc, err := hostsfile.Parse(path)

	if err != nil {
		t.Fatalf("hostsfile.Parse: %v", err)
	}
	if want := []hostsfile.Entry{{Host: "8.8.8.8"}, {Host: "192.0.2.1"}}; !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("hosts = %v, want %v (inline entries first, then includes)", doc.Hosts, want)
	}
	wantGroups := []hostsfile.Group{
		{Name: "Branches", Hosts: []hostsfile.Entry{{Host: "198.51.100.1", Name: "branch-a"}, {Host: "198.51.100.2", Name: "branch-b"}}},
		{Name: "Mixed", Hosts: []hostsfile.Entry{{Host: "1.1.1.1"}, {Host: "192.0.2.1"}}},
	}
	if !reflect.DeepEqual(doc.Groups, wantGroups) {
		t.Fatalf("groups = %#v, want %#v", doc.Groups, wantGroups)
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Fatalf("an include-only group must pass validation: %v", err)
	}
}

func TestValidateHostsDoc_Names(t *testing.T) {
	tests := []struct {
		name    string
		doc     hostsfile.Document
		wantErr string
	}{
		{
			name: "distinct names are valid",
			doc: hostsfile.Document{
				Hosts:  []hostsfile.Entry{{Host: "10.0.0.1", Name: "a"}, {Host: "10.0.0.1", Name: "b", DSCP: "EF"}},
				Groups: []hostsfile.Group{{Name: "G", Hosts: []hostsfile.Entry{{Host: "10.0.0.2", Name: "c"}}}},
			},
		},
		{
			name: "duplicate name across groups",
			doc: hostsfile.Document{
				Hosts:  []hostsfile.Entry{{Host: "10.0.0.1", Name: "core"}},
				Groups: []hostsfile.Group{{Name: "G", Hosts: []hostsfile.Entry{{Host: "10.0.0.2", Name: "core"}}}},
			},
			wantErr: `duplicate name "core"`,
		},
		{
			name: "name equal to another entry's host",
			doc: hostsfile.Document{
				Hosts: []hostsfile.Entry{{Host: "10.0.0.1", Name: "10.0.0.2"}, {Host: "10.0.0.2"}},
			},
			wantErr: `name "10.0.0.2" is also used as a host`,
		},
		{
			name: "name equal to its own host is allowed",
			doc: hostsfile.Document{
				Hosts: []hostsfile.Entry{{Host: "gw", Name: "gw"}},
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

func TestBuildHostsAndGroups_CarriesNames(t *testing.T) {
	specs, _ := buildHostsAndGroups(
		[]hostsfile.Entry{{Host: "10.0.0.1", Name: "core"}},
		[]hostsfile.Group{{Name: "G", Hosts: []hostsfile.Entry{{Host: "10.0.0.2", Name: "edge", DSCP: "EF"}}}},
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
