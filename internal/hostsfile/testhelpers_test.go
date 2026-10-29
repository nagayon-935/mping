package hostsfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// netBoxCSV returns a minimal NetBox IP addresses export with one
// "IP Address,Status,DNS Name" row per argument.
func netBoxCSV(rows ...string) string {
	return "IP Address,Status,DNS Name\n" + strings.Join(rows, "\n") + "\n"
}

// writeHostsYAML writes content to hosts.yaml in a fresh temp dir and returns
// its path.
func writeHostsYAML(t *testing.T, content string) string {
	t.Helper()
	return writeTestFile(t, t.TempDir(), "hosts.yaml", content)
}
