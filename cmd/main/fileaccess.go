package main

import (
	"github.com/nagayon-935/mping/internal/privilege"
	"github.com/nagayon-935/mping/internal/report"
)

// asRealUser runs file I/O as the invoking user. A setuid-root install
// would otherwise write any path the user names for a report with root's
// permissions; with it, the kernel applies the invoking user's permissions
// instead. Hosts and include files are read the same way inside
// internal/hostsfile. Tests swap it to verify that every file operation goes
// through it.
var asRealUser = privilege.AsRealUser

// writeReportFile saves a report as the invoking user.
func writeReportFile(path, format string, r report.Report) error {
	return asRealUser(func() error { return report.Write(path, format, r) })
}
