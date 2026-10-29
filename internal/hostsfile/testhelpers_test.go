package hostsfile

import (
	"os"
	"path/filepath"
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

// writeHostsYAML writes content to hosts.yaml in a fresh temp dir and returns
// its path.
func writeHostsYAML(t *testing.T, content string) string {
	t.Helper()
	return writeTestFile(t, t.TempDir(), "hosts.yaml", content)
}
