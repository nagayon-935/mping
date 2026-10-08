//go:build unix

package privilege

import (
	"os"
	"syscall"
)

// sys uses the real process credentials. On Linux, Go applies
// seteuid/setegid to every thread of the process.
var sys = credentials{
	getuid: os.Getuid, geteuid: os.Geteuid,
	getgid: os.Getgid, getegid: os.Getegid,
	seteuid: syscall.Seteuid, setegid: syscall.Setegid,
}
