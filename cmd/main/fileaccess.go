package main

import (
	"os"

	"github.com/nagayon-935/mping/internal/privilege"
	"github.com/nagayon-935/mping/internal/report"
)

// asRealUser runs file I/O as the invoking user. A setuid-root install
// would otherwise read and write any path the user names (-f, include:,
// -o, -j, report saves) with root's permissions; with it, the kernel
// applies the invoking user's permissions instead. Tests swap it to verify
// that every file operation goes through it.
var asRealUser = privilege.AsRealUser

// readFileAsRealUser reads path as the invoking user. Errors are returned
// unwrapped so each caller keeps its own message.
func readFileAsRealUser(path string) ([]byte, error) {
	var data []byte
	err := asRealUser(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

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
