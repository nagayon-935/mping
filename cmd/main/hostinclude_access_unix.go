//go:build unix

package main

import "syscall"

// accessReadOK is access(2)'s R_OK, which is 4 on every POSIX system.
const accessReadOK = 4

// realUserCanRead reports whether the real (invoking) user may read path.
// access(2) checks the real UID/GID rather than the effective ones, so under
// a setuid-root install it refuses files that only root could read. It is a
// variable so tests can simulate a denial.
var realUserCanRead = func(path string) error {
	return syscall.Access(path, accessReadOK)
}
