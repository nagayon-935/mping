package hostsfile

import (
	"reflect"
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

func TestParseHostsFile_PerHostDSCPMapping(t *testing.T) {
	// The feature's primary use case: the same destination monitored twice,
	// once per DSCP marking, side by side.
	path := writeHostsYAML(t, "hosts:\n"+
		"  - host: 2001:db8::1\n    dscp: EF\n"+
		"  - host: 2001:db8::1\n    dscp: CS0\n"+
		"  - plain.example.com\n")

	got, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Entry{
		{Host: "2001:db8::1", DSCP: "EF"},
		{Host: "2001:db8::1", DSCP: "CS0"},
		{Host: "plain.example.com"},
	}
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
		{"host mapping missing the required 'host' key", "hosts:\n  - dscp: EF\n"},
		{"unknown key in host mapping (typo of dscp)", "hosts:\n  - host: example.com\n    dscpp: EF\n"},
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
