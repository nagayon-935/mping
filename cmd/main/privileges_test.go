package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRejectSetIDCredentials(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		uid, euid, gid, egid int
		allowed              bool
	}{
		{"normal", 1000, 1000, 1000, 1000, true},
		{"explicit root or sudo", 0, 0, 0, 0, true},
		{"unsupported platform IDs", -1, -1, -1, -1, true},
		{"setuid root", 1000, 0, 1000, 1000, false},
		{"setgid", 1000, 1000, 1000, 0, false},
		{"setuid other user", 1000, 1001, 1000, 1000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkPrivileges(tc.uid, tc.euid, tc.gid, tc.egid); (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
		})
	}
}

// Execute the real installer with isolated command shims. No root privileges,
// system installation paths, actual chown or setcap operations are used.
func TestInstallerNeverGrantsSetuid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	installer, err := filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		os   string
		caps bool
	}{{"Darwin", false}, {"Linux", false}, {"Linux", true}} {
		t.Run(tc.os+map[bool]string{true: "/capabilities", false: "/sudo"}[tc.caps], func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "tools")
			dest := filepath.Join(dir, "installed")
			for _, path := range []string{bin, dest} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeTool := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			writeTool("id", "echo 0")
			writeTool("uname", "echo "+tc.os)
			writeTool("chown", "exit 0")
			writeTool("codesign", "exit 0")
			for _, name := range []string{"cp", "chmod"} {
				path, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.caps {
				writeTool("setcap", "printf '%s\\n' \"$1\" > \"$INSTALL_DIR/capability\"")
			}
			if err := os.WriteFile(filepath.Join(dir, "mping"), []byte("binary placeholder"), 0755); err != nil {
				t.Fatal(err)
			}
			installed := filepath.Join(dest, "mping")
			if err := os.WriteFile(installed, []byte("old binary"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(installed, 0755|os.ModeSetuid|os.ModeSetgid); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(shell, installer)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin, "INSTALL_DIR="+dest)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("installer: %v\n%s", err, output)
			}
			info, err := os.Stat(installed)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
				t.Fatalf("installer left set-ID bits: %v", info.Mode())
			}
			if tc.caps {
				data, err := os.ReadFile(filepath.Join(dest, "capability"))
				if err != nil || strings.TrimSpace(string(data)) != "cap_net_raw+ep" {
					t.Fatalf("missing raw socket capability: %s %v", data, err)
				}
			} else if !strings.Contains(string(output), "sudo") {
				t.Fatalf("installer did not explain sudo requirement: %s", output)
			}
		})
	}
}
