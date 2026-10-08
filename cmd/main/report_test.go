package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/report"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

func TestRunConnectsReportSavingToEffectiveSession(t *testing.T) {
	previousPinger, previousUI := newPinger, uiRun
	t.Cleanup(func() { newPinger, uiRun = previousPinger, previousUI })
	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController { return &fakePinger{} }
	path := filepath.Join(t.TempDir(), "session.json")
	uiRun = func(options ui.RunOptions) error {
		if options.OnSaveReport == nil {
			t.Fatal("save callback missing")
		}
		options.OnStop()
		return options.OnSaveReport(path, "json", 0)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"-4", "-S", "127.0.0.1", "--duration", "1m", "192.0.2.1"}, &out, &errOut); code != 0 {
		t.Fatalf("run failed: %s", errOut.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r report.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Settings.Duration != "1m0s" || r.Settings.DurationDeadline == nil || r.Settings.BoundSource != "127.0.0.1" || r.Settings.Network != "ip4" || r.State != "stopped" || r.SessionStartedAt.IsZero() || r.CollectionStartedAt.IsZero() || r.Targets[0].Statistics.Host != "192.0.2.1" {
		t.Fatalf("incorrect session wiring: %+v", r)
	}
}

func captureTestReport(t *testing.T, s *supervisor, id uint64) report.Report {
	t.Helper()
	var r report.Report
	if err := s.editTargets(func(s *supervisor) error {
		var err error
		r, err = s.captureReport(id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReportKeepsRemovedIdentityAndActualSettings(t *testing.T) {
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	a.IncSent()
	a.OnSuccess(time.Millisecond, 64)
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{a, b}, specs: []targetSpec{{Host: "same"}, {Host: "same"}}, groups: []ui.TargetGroup{{Name: "first", Indices: []int{0}}}, interval: 2 * time.Second, timeout: 3 * time.Second, packetSize: 123, network: "ip4", sourceIPv4: "192.0.2.1", bind: pinger.BindConfig{Interface: "test0", Source: "192.0.2.1"}, portSpecs: []pinger.PortSpec{{Port: 443, Protocol: "tcp"}}, config: config{packetSize: 999, intervalMs: 999}})
	s.httpChecker = pinger.NewHTTPChecker([]string{"https://same.example/health"}, time.Second, time.Second, pinger.BindConfig{})
	s.httpChecker.Results()[0].SetResult(200, 2*time.Millisecond, nil)
	s.Start()
	t.Cleanup(s.Shutdown)
	if err := s.deleteTargetID(a.ID); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	all := captureTestReport(t, s, 0)
	if all.Scope != "session" || all.State != "stopped" || len(all.Targets) != 1 || all.Targets[0].Statistics.ID != b.ID || len(all.RemovedTargets) != 1 {
		t.Fatalf("incorrect report membership: %+v", all)
	}
	if len(all.HTTPChecks) != 1 || all.HTTPChecks[0].UpCount != 1 || all.HTTPChecks[0].LastRTTMs != 2 {
		t.Fatal("independent HTTP results missing")
	}
	removed := all.RemovedTargets[0]
	if removed.Statistics.ID != a.ID || removed.Statistics.Recv != 1 || removed.Group != "first" || removed.RemovedAt == nil {
		t.Fatalf("removed target was not frozen: %+v", removed)
	}
	if all.Settings.PayloadBytes != 123 || all.Settings.IntervalMS != 2000 || all.Settings.TimeoutMS != 3000 || all.Settings.BoundSource != "192.0.2.1" || all.Settings.Ports[0] != "443/tcp" || all.CaptureCompletedAt.Before(all.CapturedAt) {
		t.Fatal("report used stale configuration")
	}
	one := captureTestReport(t, s, b.ID)
	if one.Scope != "target" || len(one.RemovedTargets) != 0 || len(one.HTTPChecks) != 0 || len(one.Targets) != 1 {
		t.Fatal("selected target report included unrelated results")
	}
	if err := s.editTargets(func(s *supervisor) error { _, err := s.captureReport(a.ID); return err }); err == nil {
		t.Fatal("removed selection accepted")
	}
}

func TestReportBoundsArchiveAndRejectsActiveWriterPaths(t *testing.T) {
	dir := t.TempDir()
	reserved := filepath.Join(dir, "live.json")
	base := stats.NewTargetStats("base")
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{base}, specs: []targetSpec{{Host: "base"}}, reservedOutputs: []string{reserved}})
	s.Start()
	t.Cleanup(s.Shutdown)
	if err := s.editTargets(func(s *supervisor) error {
		for i := 0; i < maxRemovedTargets+3; i++ {
			s.archiveTarget(stats.NewTargetStats("removed"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := captureTestReport(t, s, 0)
	if len(r.RemovedTargets) != maxRemovedTargets || r.RemovedTargetsDropped != 3 {
		t.Fatal("archive retention was not reported")
	}
	if err := s.saveReport(reserved, "json", 0); err == nil {
		t.Fatal("active output path accepted")
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if err := s.saveReport(filepath.Join(alias, "live.json"), "text", 0); err == nil {
		t.Fatal("aliased active output path accepted")
	}
	if _, err := os.Stat(reserved); !os.IsNotExist(err) {
		t.Fatal("reserved path was created")
	}
	path := filepath.Join(dir, "report.json")
	if err := s.saveReport(path, "json", 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved report.Report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.RemovedTargetsDropped != 3 || saved.Targets[0].Statistics.ID != base.ID {
		t.Fatal("saved report lost snapshot data")
	}
}

func TestReportsSerializeWithLiveEditsAndReset(t *testing.T) {
	base := stats.NewTargetStats("base")
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{base}, specs: []targetSpec{{Host: "base"}}, makePinger: func(int) pingerController { return newLiveFakePinger() }})
	s.Start()
	t.Cleanup(s.Shutdown)
	if err := s.startPinger(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 15; i++ {
			if err := s.addHost("192.0.2.5"); err != nil {
				errors <- err
				return
			}
			if err := s.deleteHost("192.0.2.5"); err != nil {
				errors <- err
				return
			}
			s.resetStats()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 15; i++ {
			file, err := os.CreateTemp(dir, "snapshot-*.json")
			if err != nil {
				errors <- err
				return
			}
			path := file.Name()
			file.Close()
			os.Remove(path)
			if err := s.saveReport(path, "json", 0); err != nil {
				errors <- err
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				errors <- err
				return
			}
			var saved report.Report
			if err := json.Unmarshal(data, &saved); err != nil {
				errors <- err
				return
			}
			seen := map[uint64]bool{}
			for _, target := range append(saved.Targets, saved.RemovedTargets...) {
				if seen[target.Statistics.ID] {
					t.Error("target appeared twice in snapshot")
				}
				seen[target.Statistics.ID] = true
			}
			if !seen[base.ID] {
				t.Error("surviving target missing")
			}
		}
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
