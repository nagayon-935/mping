package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/nagayon-935/mping/internal/privilege"
	"github.com/nagayon-935/mping/internal/report"
	"github.com/nagayon-935/mping/internal/stats"
)

// maxRemovedTargets bounds the report archive of targets removed this iteration.
const maxRemovedTargets = 128

// groupNameForIndex returns the last matching group's name, or an empty string.
func (s *supervisor) groupNameForIndex(targetIdx int) string {
	var name string
	for _, group := range s.cfg.groups {
		if slices.Contains(group.Indices, targetIdx) {
			name = group.Name
		}
	}
	return name
}

func (s *supervisor) archiveTarget(target *stats.TargetStats) {
	record := s.newReportTarget(target)
	if i := slices.Index(s.cfg.targets, target); i >= 0 {
		record.Group = s.groupNameForIndex(i)
	}
	now := time.Now().UTC()
	record.RemovedAt = &now
	s.removed = append(s.removed, record)
	if len(s.removed) > maxRemovedTargets {
		s.removed = append([]report.Target(nil), s.removed[len(s.removed)-maxRemovedTargets:]...)
		s.removedDropped++
	}
}

// newReportTarget captures t with the source its route uses.
func (s *supervisor) newReportTarget(t *stats.TargetStats) report.Target {
	record := report.NewTarget(t)
	record.SourceIP = s.cfg.sources.forDst(record.Statistics.IP)
	return record
}

// captureReport runs on the supervisor command loop, so target membership,
// reset operations and settings cannot change midway through collection.
// Measurements continue; counters and events share a target lock, while
// port and MTR results use their own locks within the capture interval.
func (s *supervisor) captureReport(selectedID uint64) (report.Report, error) {
	r := report.Report{
		SchemaVersion:       1,
		SessionStartedAt:    s.cfg.startedAt,
		CollectionStartedAt: s.cfg.collectionStartedAt,
		CapturedAt:          time.Now().UTC(),
		State:               s.state.String(),
		Scope:               "session",
		Targets:             make([]report.Target, 0, len(s.cfg.targets)),
	}
	if p, ok := s.livePinger(); ok {
		r.PingFinished = p.WorkersFinished()
	}
	cfg := s.cfg.config
	policy := healthPolicy(cfg)
	r.Settings = report.Settings{
		IntervalMS: s.cfg.interval.Milliseconds(), TimeoutMS: s.cfg.timeout.Milliseconds(),
		PayloadBytes: s.cfg.packetSize, Interface: s.cfg.bind.Interface, BoundSource: s.cfg.bind.Source,
		SourceIPv4Hint: s.cfg.sourceIPv4, SourceIPv6Hint: s.cfg.sourceIPv6, Network: s.cfg.network,
		Count: cfg.count, Duration: s.cfg.durationLimit.String(),
		DNSServer: cfg.dnsServer, ResolveAll: cfg.resolveAll,
		Traceroute: cfg.trace, MTR: cfg.mtr, PMTUDiscovery: cfg.mtuEnabled,
		HTTPURLs:            append([]string(nil), s.cfg.httpURLs...),
		HealthFailures:      policy.Failures,
		HealthRecoveries:    policy.Recoveries,
		HealthWindowSeconds: policy.Window.Seconds(),
		HTTPFollowRedirects: cfg.httpFollowRedirects,
		Thresholds:          thresholdsMap(cfg.thresholds),
	}
	if !s.cfg.durationDeadline.IsZero() {
		deadline := s.cfg.durationDeadline.UTC()
		r.Settings.DurationDeadline = &deadline
	}
	for _, port := range s.cfg.portSpecs {
		r.Settings.Ports = append(r.Settings.Ports, fmt.Sprintf("%d/%s", port.Port, port.Protocol))
	}
	for i, t := range s.cfg.targets {
		if selectedID != 0 && t.ID() != selectedID {
			continue
		}
		record := s.newReportTarget(t)
		record.Group = s.groupNameForIndex(i)
		r.Targets = append(r.Targets, record)
	}
	if selectedID != 0 {
		if len(r.Targets) == 0 {
			return report.Report{}, fmt.Errorf("selected target is no longer active")
		}
		r.Scope = "target"
	} else {
		r.RemovedTargets = append([]report.Target(nil), s.removed...)
		r.RemovedTargetsDropped = s.removedDropped
		if s.httpChecker != nil {
			r.HTTPChecks = stats.HTTPCheckSummaries(s.httpChecker.Results())
		}
	}
	r.CaptureCompletedAt = time.Now().UTC()
	return r, nil
}

// snapshotReport captures a report through the command loop so membership and
// settings stay consistent. Callers perform file I/O after the command returns.
func (s *supervisor) snapshotReport(selectedID uint64) (snapshot report.Report, err error) {
	err = s.editTargets(func(s *supervisor) error {
		var err error
		snapshot, err = s.captureReport(selectedID)
		return err
	})
	return
}

func (s *supervisor) saveReport(path, format string, selectedID uint64) error {
	if format != "csv" && format != "json" {
		return fmt.Errorf("unsupported report format %q", format)
	}
	snapshot, err := s.snapshotReport(selectedID)
	if err != nil {
		return err
	}
	// File I/O happens outside the supervisor and holds no measurement locks.
	return writeReportFile(path, format, snapshot)
}

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
