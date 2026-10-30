package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// startUnprivileged starts cmd with no_new_privs, so exec cannot give the
// web process the file capabilities or setuid root mping was installed
// with. The flag is per thread and inherited by a child forked from it, so
// it is set on a locked thread that is retired afterwards: it cannot be
// cleared again. As on other Unix systems, the process also gets a process
// group of its own (see web_process_start_unix.go).
func startUnprivileged(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	errc := make(chan error, 1)
	go func() {
		// Never unlocked: the goroutine's exit ends the thread with it.
		runtime.LockOSThread()
		if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
			errc <- fmt.Errorf("set no_new_privs: %w", err)
			return
		}
		errc <- cmd.Start()
	}()
	return <-errc
}
