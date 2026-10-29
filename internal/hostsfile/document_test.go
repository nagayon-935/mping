package hostsfile

import (
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

func TestParseHostsFile_RejectsRemovedSettings(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"top-level dscp", "dscp: EF\nhosts:\n  - example.com\n", `line 1: "dscp" was removed`},
		{"per-host dscp", "hosts:\n  - host: 2001:db8::1\n    dscp: EF\n", `line 3: "dscp" was removed`},
		{"group host dscp", "groups:\n  - name: qos\n    hosts:\n      - {host: \"::1\", dscp: EF}\n", `line 4: "dscp" was removed`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(writeHostsYAML(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse error = %v, want it to contain %q", err, tt.want)
			}
		})
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
