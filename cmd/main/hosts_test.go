package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseHostsFile_List(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("- example.com\n- 1.1.1.1\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	got, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	want := []string{"example.com", "1.1.1.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts: got %v, want %v", got, want)
	}
}

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
	want := []string{"example.com", "8.8.8.8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts: got %v, want %v", got, want)
	}
}
