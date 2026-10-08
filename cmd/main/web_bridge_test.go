package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
)

// stubRunSeams swaps newPinger/uiRun/webStart for the duration of a test.
// webStart is wrapped so the real server binds a kernel-chosen port instead
// of the configured one, keeping tests independent of what is free locally.
func stubRunSeams(t *testing.T, ui func(ui.RunOptions) error) (started func() *web.Server) {
	t.Helper()
	origPinger, origUI, origWeb := newPinger, uiRun, webStart
	t.Cleanup(func() { newPinger, uiRun, webStart = origPinger, origUI, origWeb })

	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController { return &fakePinger{} }
	uiRun = ui
	var srv *web.Server
	webStart = func(opts web.Options) (*web.Server, error) {
		opts.Port = 0
		s, err := web.Start(opts)
		srv = s
		return s, err
	}
	return func() *web.Server { return srv }
}

func TestRunWithWebServesLiveSnapshotWhileUIRuns(t *testing.T) {
	var started func() *web.Server
	var status int
	var snap web.SnapshotResponse
	var logs []string
	started = stubRunSeams(t, func(opts ui.RunOptions) error {
		logs = opts.InitialLogs
		resp, err := http.Get(started().URL() + "api/v1/snapshot")
		if err != nil {
			t.Errorf("GET snapshot: %v", err)
			return nil
		}
		defer resp.Body.Close()
		status = resp.StatusCode
		if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
			t.Errorf("decode snapshot: %v", err)
		}
		return nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "--mtr", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if status != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", status)
	}
	if snap.Reloading {
		t.Error("reloading = true while the UI iteration is running")
	}
	if len(snap.Snapshot.Targets) != 1 || snap.Snapshot.Targets[0].Host != "example.com" {
		t.Errorf("targets = %+v, want example.com", snap.Snapshot.Targets)
	}
	if !snap.Meta.Features.MTR {
		t.Error("meta.features.mtr = false, want true with --mtr")
	}
	if !containsSubstring(logs, "Web UI: "+started().URL()) {
		t.Errorf("initial logs %q do not announce %s", logs, started().URL())
	}
}

func TestRunWithWebClosesServerOnExit(t *testing.T) {
	started := stubRunSeams(t, func(ui.RunOptions) error { return nil })

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	client := &http.Client{Timeout: time.Second}
	if resp, err := client.Get(started().URL()); err == nil {
		resp.Body.Close()
		t.Fatal("web server still answering after run returned")
	}
}

func TestRunWithoutWebDoesNotStartServer(t *testing.T) {
	stubRunSeams(t, func(ui.RunOptions) error { return nil })
	called := false
	webStart = func(web.Options) (*web.Server, error) {
		called = true
		return nil, errors.New("unexpected")
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if called {
		t.Fatal("web server started without --web")
	}
}

func TestRunExitsWhenWebServerCannotStart(t *testing.T) {
	stubRunSeams(t, func(ui.RunOptions) error {
		t.Error("UI started even though the web server failed")
		return nil
	})
	webStart = func(web.Options) (*web.Server, error) {
		return nil, errors.New("address already in use")
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "address already in use") {
		t.Fatalf("stderr = %q, want the listen error", errOut.String())
	}
}

func TestWebMetaReflectsConfig(t *testing.T) {
	cfg := config{
		intervalMs: 500, trace: true, mtr: true, asnEnabled: true, ptrEnabled: true,
		httpURLs: []string{"https://example.com/"}, dscp: "EF",
		thresholds: ui.Thresholds{
			RTTWarn: 40 * time.Millisecond, RTTCrit: 150 * time.Millisecond,
			JitterWarn: 5 * time.Millisecond, JitterCrit: 25 * time.Millisecond,
			LossWarn: 10, LossCrit: 50,
		},
	}

	got := webMeta(cfg, nil, 2)

	want := web.Meta{
		IntervalMs: 500,
		Features:   web.Features{Traceroute: true, MTR: true, Port: true, HTTP: true, ASN: true, PTR: true, DSCP: true},
		Thresholds: web.Thresholds{RTTWarnMs: 40, RTTCritMs: 150, JitterWarnMs: 5, JitterCritMs: 25, LossWarnPct: 10, LossCritPct: 50},
	}
	if got.IntervalMs != want.IntervalMs || got.Features != want.Features || got.Thresholds != want.Thresholds {
		t.Fatalf("webMeta = %+v, want %+v", got, want)
	}
}

func TestWebMetaLeavesOptionalFeaturesOffByDefault(t *testing.T) {
	got := webMeta(config{intervalMs: 1000, thresholds: ui.DefaultThresholds()}, nil, 0)

	if got.Features != (web.Features{}) {
		t.Fatalf("features = %+v, want all off", got.Features)
	}
}

func TestWebGroupsMapsIndicesToTargetIDs(t *testing.T) {
	a, b, c := stats.NewTargetStats("a"), stats.NewTargetStats("b"), stats.NewTargetStats("c")
	set := ui.TargetSet{
		Targets: []*stats.TargetStats{a, b, c},
		Groups: []ui.TargetGroup{
			{Name: "core", Indices: []int{0, 2}},
			{Name: "edge", Indices: []int{1, 7, -1}},
		},
	}

	got := webGroups(set)

	if len(got) != 2 {
		t.Fatalf("groups = %+v, want 2", got)
	}
	if got[0].Name != "core" || len(got[0].TargetIDs) != 2 || got[0].TargetIDs[0] != a.ID || got[0].TargetIDs[1] != c.ID {
		t.Errorf("core = %+v, want IDs [%d %d]", got[0], a.ID, c.ID)
	}
	if got[1].Name != "edge" || len(got[1].TargetIDs) != 1 || got[1].TargetIDs[0] != b.ID {
		t.Errorf("edge = %+v, want only ID %d (out-of-range indices dropped)", got[1], b.ID)
	}
}

func TestWebGroupsIsEmptyWithoutGroups(t *testing.T) {
	got := webGroups(ui.TargetSet{Targets: []*stats.TargetStats{stats.NewTargetStats("a")}})

	if len(got) != 0 {
		t.Fatalf("groups = %+v, want none", got)
	}
}

func containsSubstring(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestParseArgsWebDefaultsToOffOnPort8080(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.webEnabled || cfg.webPort != 8080 {
		t.Fatalf("web = (%v, %d), want (false, 8080)", cfg.webEnabled, cfg.webPort)
	}
}

func TestParseArgsRejectsOutOfRangeWebPort(t *testing.T) {
	for _, port := range []string{"0", "-1", "65536"} {
		t.Run(port, func(t *testing.T) {
			_, _, _, _, err := parseArgs([]string{"--web", "--web-port", port, "example.com"})

			if err == nil || !strings.Contains(err.Error(), "--web-port") {
				t.Fatalf("err = %v, want a --web-port range error", err)
			}
		})
	}
}
