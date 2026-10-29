package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

func TestCheckConfigExpandsTargetsAndNeverStartsMeasurementsOrWritesFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	output := filepath.Join(dir, "events.jsonl")
	t.Setenv("MPING_TEST_SECRET", "private-auth-value")
	doc := "hosts:\n  - '192.0.2.{1..3}'\ninterval: 2000\nhealth-failures: 5\nhttp-header:\n  - 'Authorization=Bearer ${MPING_TEST_SECRET}'\nevent-output: " + output + "\n"
	if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := run([]string{"--check-config", "-f", path, "--interval", "500"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code=%d: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "private-auth-value") {
		t.Fatal("secret leaked")
	}
	var body struct {
		Valid    bool `json:"valid"`
		Count    int  `json:"target_count"`
		Settings struct {
			Interval int `json:"interval_ms"`
			Failures int `json:"health_failures"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Valid || body.Count != 3 || body.Settings.Interval != 500 || body.Settings.Failures != 5 {
		t.Fatalf("output=%s", out.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("check-config wrote event output")
	}
}

func TestCheckConfigRejectsBadMonitoringOptionsWithoutWriting(t *testing.T) {
	for _, args := range [][]string{
		{"--source=not-an-ip"}, {"--interface=mping-no-such-interface"}, {"--ipv4", "--source=::1"}, {"--health-failures=0"}, {"--health-window=0s"}, {"--health-failures=0", "--health-recoveries=0", "--health-window=0s"}, {"--health-recoveries=10001"},
		{"--http-status=600"}, {"--http-status=399-200"}, {"--http-header=Authorization=${MPING_NO_SUCH_VARIABLE_935}"},
		{"--http-header=Bad Header=value"}, {"--http-header=X-Key=bad\r\nvalue"}, {"--http=ftp://example.com"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			full := append([]string{"--check-config", "192.0.2.1"}, args...)
			if run(full, &out, &errOut) == 0 || errOut.Len() == 0 {
				t.Fatalf("accepted %v", args)
			}
		})
	}
}

func TestLiveReloadPreservesIDsHistoryNamesAndStoppedState(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	specs := []targetSpec{{Host: "192.0.2.1", Name: "before"}, {Host: "192.0.2.2"}}
	targets := buildTargetsForIteration(specs, cfg)
	targets[0].OnSuccess(3*time.Millisecond, 64)
	firstID := targets[0].ID()
	s := newSupervisor(supervisorConfig{config: cfg, specs: specs, targets: targets})
	s.Start()
	defer s.Shutdown()
	s.cfg.targets[0].SetMeasurementState("stopped")
	updated := cfg
	updated.healthFailures = 5
	updated.thresholds.LossCrit = 90
	changed := []targetSpec{{Host: "192.0.2.3"}, {Host: "192.0.2.1", Name: "after"}}
	groups := []ui.TargetGroup{{Name: "renamed group", Indices: []int{1}}}
	applied, _, err := s.reloadLive(updated, changed, groups)
	if err != nil || !applied {
		t.Fatalf("reload=%v %v", applied, err)
	}
	set := s.liveTargets()
	v := set.Targets[1].GetView()
	if v.ID != firstID || v.Host != "after" || v.Recv != 1 || len(v.History) != 1 || v.MeasurementState != "stopped" || set.Groups[0].Name != "renamed group" {
		t.Fatalf("lost identity/history: %+v", v)
	}
	if set.Targets[0].GetView().MeasurementState != "stopped" {
		t.Fatal("new target resumed session")
	}
	if len(s.removed) != 1 {
		t.Fatal("removed target not archived")
	}
	network := updated
	network.timeoutMs++
	if ok, reason, err := s.reloadLive(network, changed, groups); ok || err != nil || !strings.Contains(reason, "timeout") {
		t.Fatalf("network change was silently applied: %v %s %v", ok, reason, err)
	}
}

func TestLiveReloadAddFailureIsAtomic(t *testing.T) {
	p := newLiveFakePinger()
	p.failAdd = true
	cfg, _, _, _, _ := parseArgs([]string{"192.0.2.1"})
	specs := []targetSpec{{Host: "192.0.2.1"}}
	targets := buildTargetsForIteration(specs, cfg)
	s := newSupervisor(supervisorConfig{config: cfg, targets: targets, specs: specs, makePinger: func(int) pingerController { return p }})
	s.Start()
	defer s.Shutdown()
	if err := s.startPinger(); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := s.reloadLive(cfg, []targetSpec{{Host: "192.0.2.1", Name: "changed"}, {Host: "192.0.2.2"}}, nil); ok || err == nil {
		t.Fatal("add failure accepted")
	}
	if got := s.liveTargets().Targets; len(got) != 1 || got[0].GetView().Host != "192.0.2.1" {
		t.Fatal("failed reload changed active targets")
	}
}

func TestEventOutputFlushesAndSharedEventsReachHeadlessLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	var errOut bytes.Buffer
	sink, closeOutput, err := startEventOutput(path, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	target := stats.NewTargetStats("router")
	logs := make(chan string, 16)
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{target}, logCh: logs, eventSink: sink})
	target.OnSuccess(time.Millisecond, 64)
	for range 3 {
		target.OnFailure("timeout")
	}
	closeOutput()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"kind":"health"`)) || !bytes.Contains(data, []byte(`"count":3`)) || errOut.Len() != 0 {
		t.Fatalf("output %s; err %s", data, errOut.String())
	}
	found := false
	for len(logs) > 0 {
		if strings.Contains(<-logs, "3 consecutive failures") {
			found = true
		}
	}
	if !found {
		t.Fatal("headless/TUI did not receive backend health transition")
	}
	_ = s
}

func TestWebStopRestartAndReportUseSupervisorWithoutReset(t *testing.T) {
	target := stats.NewTargetStats("router")
	target.OnSuccess(time.Millisecond, 64)
	s := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{target}, makePinger: func(int) pingerController { return &fakePinger{} }})
	s.Start()
	defer s.Shutdown()
	if err := s.startPinger(); err != nil {
		t.Fatal(err)
	}
	c := webController{sup: s}
	if err := c.StopMeasurements(); err != nil {
		t.Fatal(err)
	}
	if target.GetView().MeasurementState != "stopped" {
		t.Fatal("not stopped")
	}
	if err := c.RestartMeasurements(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.CaptureReport()
	if err != nil {
		t.Fatal(err)
	}
	if target.GetView().MeasurementState != "running" || snapshot.Targets[0].Statistics.Recv != 1 {
		t.Fatal("restart reset statistics")
	}
}

func TestReloadAfterCountCompletionPublishesRunningAndClearsOldNotification(t *testing.T) {
	cfg, _, _, _, _ := parseArgs([]string{"192.0.2.1"})
	specs := []targetSpec{{Host: "192.0.2.1"}}
	targets := buildTargetsForIteration(specs, cfg)
	p := newLiveFakePinger()
	s := newSupervisor(supervisorConfig{config: cfg, targets: targets, specs: specs, countLimited: true, makePinger: func(int) pingerController { return p }})
	s.Start()
	defer s.Shutdown()
	if err := s.startPinger(); err != nil {
		t.Fatal(err)
	}
	targets[0].SetMeasurementState("finished")
	s.finished <- struct{}{}
	if ok, _, err := s.reloadLive(cfg, []targetSpec{{Host: "192.0.2.1"}, {Host: "192.0.2.2"}}, nil); !ok || err != nil {
		t.Fatalf("reload=%v %v", ok, err)
	}
	if s.measurementState() != "running" || len(s.finished) != 0 || targets[0].MeasurementState() != "finished" {
		t.Fatal("a completed target hid the newly added worker")
	}
	provider := newWebProvider(s, cfg, specs, 0, nil)
	if provider.Meta().MeasurementState != "running" {
		t.Fatal("web state not synchronized")
	}
}
