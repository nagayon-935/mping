package main

import (
	"github.com/nagayon-935/mping/internal/privilege"
	"github.com/nagayon-935/mping/internal/report"
)

// asRealUser runs file I/O as the invoking user. A setuid-root install
// would otherwise read and write any path the user names (-f, include:,
// -o, -j, report saves) with root's permissions; with it, the kernel
// applies the invoking user's permissions instead. Tests swap it to verify
// that every file operation goes through it.
var asRealUser = privilege.AsRealUser

// writeReportFile saves a report as the invoking user.
func writeReportFile(path, format string, r report.Report) error {
	return asRealUser(func() error { return report.Write(path, format, r) })
}
