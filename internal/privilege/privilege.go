// Package privilege lets a setuid-root mping touch files as the user who
// invoked it while keeping root for the raw ICMP sockets it needs.
//
// File I/O runs inside AsRealUser, which temporarily switches the effective
// UID/GID to the real ones, so the kernel itself enforces the invoking
// user's permissions (no access(2)-then-open race). Effective IDs are
// process-wide, so privileged operations (opening raw sockets, binding to a
// device) run inside Privileged, which never overlaps an AsRealUser section.
package privilege

import (
	"errors"
	"fmt"
	"sync"
)

// credentials holds the process-credential calls; tests swap in a fake.
type credentials struct {
	getuid, geteuid, getgid, getegid func() int
	seteuid, setegid                 func(int) error
}

// mu is held shared by Privileged sections and exclusively by AsRealUser,
// so no privileged operation runs while privileges are dropped.
var mu sync.RWMutex

// Privileged runs fn, an operation that needs mping's elevated privileges,
// waiting for any in-progress AsRealUser section to finish first.
// Privileged sections may run concurrently with each other.
func Privileged(fn func() error) error {
	mu.RLock()
	defer mu.RUnlock()
	return fn()
}

// AsRealUser runs fn with the effective UID/GID set to the real ones when
// the process runs setuid/setgid, restoring them afterwards. When the IDs
// already match (ordinary run, sudo, setcap, Windows) it just runs fn.
// If privileges can't be dropped, fn is not run. A failure to restore them
// is returned joined with fn's own error.
func AsRealUser(fn func() error) (err error) {
	uid, euid := sys.getuid(), sys.geteuid()
	gid, egid := sys.getgid(), sys.getegid()
	if uid == euid && gid == egid {
		return fn()
	}
	mu.Lock()
	defer mu.Unlock()

	// Drop the group first: changing it requires the privileges the
	// user switch gives up.
	if err := sys.setegid(gid); err != nil {
		return fmt.Errorf("drop privileges: %w", err)
	}
	if err := sys.seteuid(uid); err != nil {
		if rerr := sys.setegid(egid); rerr != nil {
			return fmt.Errorf("drop privileges: %w (restoring group also failed: %v)", err, rerr)
		}
		return fmt.Errorf("drop privileges: %w", err)
	}
	defer func() {
		if rerr := restore(euid, egid); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}()
	return fn()
}

// restore regains the original effective IDs, user first so that the
// group change is permitted again.
func restore(euid, egid int) error {
	if err := sys.seteuid(euid); err != nil {
		return fmt.Errorf("restore privileges: %w", err)
	}
	if err := sys.setegid(egid); err != nil {
		return fmt.Errorf("restore privileges: %w", err)
	}
	return nil
}
