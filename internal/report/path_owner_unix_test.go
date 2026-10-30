//go:build unix

package report

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// File mode bits and Stat_t ownership are POSIX concepts; Windows has neither.
func TestReportFileIsPrivateAndOwnedByInvoker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.csv")
	if err := Write(path, "csv", Report{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("report mode=%o", info.Mode().Perm())
	}
	owner, err := ownerForCaller(os.Getuid(), os.Getgid(), os.Geteuid(), os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if int(stat.Uid) != owner.uid {
		t.Fatalf("report uid=%d want=%d", stat.Uid, owner.uid)
	}
	if os.Geteuid() == 0 && int(stat.Gid) != owner.gid {
		t.Fatalf("report gid=%d want=%d", stat.Gid, owner.gid)
	}
}
