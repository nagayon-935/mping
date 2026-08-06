package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseHostsFile_Mapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n  - example.com\n  - 8.8.8.8\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	got, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	want := []hostEntry{{Host: "example.com"}, {Host: "8.8.8.8"}}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts: got %v, want %v", got.Hosts, want)
	}
}

func TestParseHostsFile_PerHostDSCPMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	// The feature's primary use case: the same destination monitored twice,
	// once per DSCP marking, side by side.
	content := "hosts:\n" +
		"  - host: 2001:db8::1\n    dscp: EF\n" +
		"  - host: 2001:db8::1\n    dscp: CS0\n" +
		"  - plain.example.com\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	got, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	want := []hostEntry{
		{Host: "2001:db8::1", DSCP: "EF"},
		{Host: "2001:db8::1", DSCP: "CS0"},
		{Host: "plain.example.com"},
	}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts: got %v, want %v", got.Hosts, want)
	}
}

func TestParseHostsFile_HostMappingMissingHostIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	content := "hosts:\n  - dscp: EF\n" // missing required 'host' key
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := parseHostsFile(path)
	if err == nil {
		t.Fatal("parseHostsFile: expected error for host mapping missing 'host', got nil")
	}
}

func TestParseHostsFile_HostMappingUnknownKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	content := "hosts:\n  - host: example.com\n    dscpp: EF\n" // typo of "dscp"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := parseHostsFile(path)
	if err == nil {
		t.Fatal("parseHostsFile: expected error for unknown key in host mapping, got nil")
	}
}

func TestParseHostsFile_EmptyFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	got, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile on empty file: unexpected error: %v", err)
	}
	if len(got.Hosts) != 0 {
		t.Errorf("hosts: got %v, want empty", got.Hosts)
	}
}

func TestParseHostsFile_UnknownKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	// "interva" is a typo of "interval" and must be surfaced as an error
	// rather than silently ignored.
	content := "hosts:\n  - example.com\ninterva: 500\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := parseHostsFile(path)
	if err == nil {
		t.Fatal("parseHostsFile: expected error for unknown key, got nil")
	}
}

func TestParseHostsFile_UnknownGroupKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	content := "groups:\n  - name: web\n    host:\n      - example.com\n" // "host" typo of "hosts"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := parseHostsFile(path)
	if err == nil {
		t.Fatal("parseHostsFile: expected error for unknown key in nested group, got nil")
	}
}
