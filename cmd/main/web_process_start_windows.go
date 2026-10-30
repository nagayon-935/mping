package main

import "os/exec"

// startUnprivileged starts cmd; Windows has no setuid or capabilities for
// the web process to give up.
func startUnprivileged(cmd *exec.Cmd) error { return cmd.Start() }
