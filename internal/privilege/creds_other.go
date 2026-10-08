//go:build !unix

package privilege

import "errors"

var errUnsupported = errors.New("changing effective IDs is not supported on this platform")

// sys reports matching IDs (os.Getuid and friends return -1 on Windows), so
// AsRealUser never tries to change them here.
var sys = credentials{
	getuid: func() int { return -1 }, geteuid: func() int { return -1 },
	getgid: func() int { return -1 }, getegid: func() int { return -1 },
	seteuid: func(int) error { return errUnsupported },
	setegid: func(int) error { return errUnsupported },
}
