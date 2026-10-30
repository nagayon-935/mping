//go:build unix && !linux

package main

import (
	"os/exec"
	"syscall"
)

// startUnprivileged starts cmd in a process group of its own, so signals
// the terminal sends mping's group (Ctrl-C before the TUI takes the
// keyboard) do not end the web UI alone; it exits when mping closes its
// stdin. The web process drops any setuid root itself (runWebProcess).
func startUnprivileged(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd.Start()
}
