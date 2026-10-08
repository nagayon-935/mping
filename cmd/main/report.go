package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/nagayon-935/mping/internal/report"
	"github.com/nagayon-935/mping/internal/stats"
)

const maxRemovedTargets = 128

func (s *supervisor) archiveTarget(target *stats.TargetStats) {
	record := report.NewTarget(target)
	for i, t := range s.cfg.targets {
		if t != target {
			continue
		}
		for _, group := range s.cfg.groups {
			for _, idx := range group.Indices {
				if idx == i {
					record.Group = group.Name
				}
			}
		}
	}
	now := time.Now().UTC()
	record.RemovedAt = &now
	s.removed = append(s.removed, record)
	if len(s.removed) > maxRemovedTargets {
		s.removed = append([]report.Target(nil), s.removed[len(s.removed)-maxRemovedTargets:]...)
		s.removedDropped++
	}
}

// captureReport runs on the supervisor command loop, so target membership,
// reset operations and settings cannot change midway through collection.
// Measurements continue; counters and events share a target lock, while
// port and MTR results use their own locks within the capture interval.
func (s *supervisor) captureReport(selectedID uint64) (report.Report, error) {
	r := report.Report{SchemaVersion: 1, SessionStartedAt: s.cfg.startedAt, CollectionStartedAt: s.cfg.collectionStartedAt, CapturedAt: time.Now().UTC(), State: s.state.String(), Scope: "session", Targets: make([]report.Target, 0, len(s.cfg.targets))}
	if p, ok := s.p.(dynamicPinger); ok {
		r.PingFinished = p.WorkersFinished()
	}
	cfg := s.cfg.config
	thresholds := cfg.thresholds
	r.Settings = report.Settings{
		IntervalMS: s.cfg.interval.Milliseconds(), TimeoutMS: s.cfg.timeout.Milliseconds(),
		PayloadBytes: s.cfg.packetSize, Interface: s.cfg.bind.Interface, BoundSource: s.cfg.bind.Source,
		SourceIPv4Hint: s.cfg.sourceIPv4, SourceIPv6Hint: s.cfg.sourceIPv6, Network: s.cfg.network,
		Count: cfg.count, Duration: s.cfg.durationLimit.String(), DSCP: cfg.dscp,
		DNSServer: cfg.dnsServer, ResolveAll: cfg.resolveAll,
		Traceroute: s.cfg.traceEnabled, MTR: s.cfg.mtrEnabled, PMTUDiscovery: cfg.mtuEnabled,
		HTTPURLs: append([]string(nil), s.cfg.httpURLs...),
		Thresholds: map[string]float64{
			"rtt_warn_ms":    float64(thresholds.RTTWarn) / float64(time.Millisecond),
			"rtt_crit_ms":    float64(thresholds.RTTCrit) / float64(time.Millisecond),
			"jitter_warn_ms": float64(thresholds.JitterWarn) / float64(time.Millisecond),
			"jitter_crit_ms": float64(thresholds.JitterCrit) / float64(time.Millisecond),
			"loss_warn_pct":  thresholds.LossWarn, "loss_crit_pct": thresholds.LossCrit,
		},
	}
	if !s.cfg.durationDeadline.IsZero() {
		deadline := s.cfg.durationDeadline.UTC()
		r.Settings.DurationDeadline = &deadline
	}
	for _, port := range s.cfg.portSpecs {
		r.Settings.Ports = append(r.Settings.Ports, fmt.Sprintf("%d/%s", port.Port, port.Protocol))
	}
	for i, t := range s.cfg.targets {
		if selectedID != 0 && t.ID != selectedID {
			continue
		}
		record := report.NewTarget(t)
		for _, group := range s.cfg.groups {
			for _, idx := range group.Indices {
				if idx == i {
					record.Group = group.Name
				}
			}
		}
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
		var views []stats.HTTPCheckView
		if s.httpChecker != nil {
			for _, check := range s.httpChecker.Results() {
				views = append(views, check.GetView())
			}
		}
		r.HTTPChecks = stats.BuildSnapshotFromViews(nil, views).HTTPChecks
	}
	r.CaptureCompletedAt = time.Now().UTC()
	return r, nil
}

func canonicalOutputPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err == nil {
		abs = filepath.Join(parent, filepath.Base(abs))
	}
	return abs
}

func (s *supervisor) saveReport(path, format string, selectedID uint64) error {
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported report format %q", format)
	}
	var snapshot report.Report
	err := s.editTargets(func(s *supervisor) error {
		reserved := append([]string{s.cfg.config.outputFile, s.cfg.config.jsonOutputFile}, s.cfg.reservedOutputs...)
		for _, active := range reserved {
			if active != "" && canonicalOutputPath(active) == canonicalOutputPath(path) {
				return fmt.Errorf("report path is reserved for an active CSV or JSON writer")
			}
		}
		var err error
		snapshot, err = s.captureReport(selectedID)
		return err
	})
	if err != nil {
		return err
	}
	// File I/O happens outside the supervisor and holds no measurement locks.
	return report.Write(path, format, snapshot)
}
