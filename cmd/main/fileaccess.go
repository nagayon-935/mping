package main

import (
	"os"

	"github.com/nagayon-935/mping/internal/privilege"
	"github.com/nagayon-935/mping/internal/report"
)

// asRealUser runs file I/O as the invoking user. A setuid-root install
// would otherwise write any path the user names (-o, -j, event output,
// report saves) with root's permissions; with it, the kernel applies the
// invoking user's permissions instead. Hosts and include files are read the
// same way inside internal/hostsfile. Tests swap it to verify that every
// file operation goes through it.
var asRealUser = privilege.AsRealUser

// openAppendAsRealUser opens path for appending as the invoking user,
// creating it with mode 0600 when missing. Errors are returned unwrapped so
// each caller keeps its own message.
func openAppendAsRealUser(path string) (*os.File, error) {
	var f *os.File
	err := asRealUser(func() error {
		var err error
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		return err
	})
	return f, err
}

// writeReportFile saves a report as the invoking user.
func writeReportFile(path, format string, r report.Report) error {
	return asRealUser(func() error { return report.Write(path, format, r) })
}
