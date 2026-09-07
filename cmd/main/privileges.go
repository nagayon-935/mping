package main

import "fmt"

// Reject setuid/setgid execution before reading configuration or opening any
// user-selected files. Explicit sudo execution and CAP_NET_RAW are supported.
func checkPrivileges(uid, euid, gid, egid int) error {
	if uid != euid || gid != egid {
		return fmt.Errorf("setuid/setgid execution is not supported; reinstall mping without these bits (basic ping runs without elevated privileges)")
	}
	return nil
}
