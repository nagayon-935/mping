package report

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPathFormat(t *testing.T) {
	for _, tt := range []struct {
		input, path, format string
		valid               bool
	}{
		{"result.json", "result.json", "json", true},
		{"result.TXT", "result.TXT", "text", true},
		{" result.JSON ", "result.JSON", "json", true},
		{"result", "result.txt", "text", true},
		{"/tmp/folder.json/result", "/tmp/folder.json/result.txt", "text", true},
		{"result.csv", "", "", false},
		{" ", "", "", false},
		{"/tmp/", "", "", false},
		{"..", "", "", false},
	} {
		path, format, err := PathFormat(tt.input)
		if path != tt.path || format != tt.format || (err == nil) != tt.valid {
			t.Fatalf("%q -> %q/%q (%v)", tt.input, path, format, err)
		}
	}
}

func TestReportOwnerFollowsInvoker(t *testing.T) {
	for _, tt := range []struct {
		name             string
		uid, gid, euid   int
		sudoUID, sudoGID string
		want             fileOwner
		valid            bool
	}{
		{"normal user ignores environment", 501, 20, 501, "0", "0", fileOwner{501, 20}, true},
		{"setuid uses real IDs", 501, 20, 0, "999", "999", fileOwner{501, 20}, true},
		{"sudo restores invoker", 0, 0, 0, "501", "20", fileOwner{501, 20}, true},
		{"direct root", 0, 0, 0, "", "", fileOwner{0, 0}, true},
		{"invalid uid", 0, 0, 0, "bad", "20", fileOwner{}, false},
		{"missing gid", 0, 0, 0, "501", "", fileOwner{}, false},
		{"negative uid", 0, 0, 0, "-1", "20", fileOwner{}, false},
		{"sentinel uid", 0, 0, 0, "4294967295", "20", fileOwner{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			owner, err := ownerForCaller(tt.uid, tt.gid, tt.euid, func(key string) string {
				if key == "SUDO_UID" {
					return tt.sudoUID
				}
				return tt.sudoGID
			})
			if (err == nil) != tt.valid || (err == nil && owner != tt.want) {
				t.Fatalf("owner=%+v err=%v", owner, err)
			}
		})
	}
}

func TestReportFileIsPrivateAndOwnedByInvoker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := Write(path, "text", Report{SchemaVersion: 1}); err != nil {
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
