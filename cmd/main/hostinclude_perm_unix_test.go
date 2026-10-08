//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadIncludeFile_UnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read any file")
	}
	path := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(path, []byte("10.0.0.1\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	_, err := readIncludeFile(path)

	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want a permission error", err)
	}
}
