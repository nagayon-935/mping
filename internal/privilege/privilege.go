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
	setuid, setgid                   func(int) error
	setgroups                        func([]int) error
	// capabilities reports Linux capabilities the process still holds; ""
	// when there are none (or the platform has none).
	capabilities func() (string, error)
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

// DropTo permanently switches the process to uid and gid, clearing its
// supplementary groups when it may, and then makes sure nothing privileged
// is left: root cannot be regained and, on Linux, no capability remains.
// It is for a process that never needs privileges again; nothing restores
// them.
func DropTo(uid, gid int) error {
	if sys.geteuid() == 0 {
		if err := sys.setgroups([]int{}); err != nil {
			return fmt.Errorf("drop supplementary groups: %w", err)
		}
	}
	// The group first: changing it needs the privileges setuid gives up.
	if err := sys.setgid(gid); err != nil {
		return fmt.Errorf("drop group: %w", err)
	}
	if err := sys.setuid(uid); err != nil {
		return fmt.Errorf("drop user: %w", err)
	}
	if sys.getuid() != uid || sys.geteuid() != uid || sys.getgid() != gid || sys.getegid() != gid {
		return errors.New("drop privileges: the process IDs did not change")
	}
	if uid != 0 && sys.seteuid(0) == nil {
		return errors.New("drop privileges: root can still be regained")
	}
	caps, err := sys.capabilities()
	if err != nil {
		return fmt.Errorf("drop privileges: %w", err)
	}
	if caps != "" {
		return fmt.Errorf("drop privileges: capabilities remain (%s)", caps)
	}
	return nil
}
