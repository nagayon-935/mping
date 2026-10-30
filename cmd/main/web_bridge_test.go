package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
)

func TestRunWithWebServesLiveSnapshotWhileUIRuns(t *testing.T) {
	var started func() webServer
	var status int
	var snap web.SnapshotResponse
	var logs []string
	var openedURL string
	started = stubRunSeams(t, func(opts ui.RunOptions) error {
		logs = opts.InitialLogs
		if opts.WebControlURL != started().ControlURL() {
			t.Error("TUI must retain the current control URL independently of Log")
		}
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
	openBrowser = func(u string) { openedURL = u }

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if status != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", status)
	}
	if snap.State != web.StateRunning {
		t.Errorf("state = %q while the UI iteration is running, want %q", snap.State, web.StateRunning)
	}
	if len(snap.Snapshot.Targets) != 1 || snap.Snapshot.Targets[0].Host != "example.com" {
		t.Errorf("targets = %+v, want example.com", snap.Snapshot.Targets)
	}
	if !containsSubstring(logs, "Web UI: "+started().URL()) {
		t.Errorf("initial logs %q do not announce %s", logs, started().URL())
	}
	if openedURL != started().ControlURL() {
		t.Errorf("openedURL = %q, want %q", openedURL, started().ControlURL())
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
	stubRunSeams(t, func(opts ui.RunOptions) error {
		if opts.WebControlURL != "" {
			t.Error("web access must be disabled when --web is absent")
		}
		return nil
	})
	called := false
	webStart = func(web.Options) (webServer, error) {
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
	webStart = func(web.Options) (webServer, error) {
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
		intervalMs: 500, trace: true, mtr: true, asnEnabled: true, ptrEnabled: true, mtuEnabled: true,
		httpURLs: []string{"https://example.com/"},
		thresholds: stats.Thresholds{
			RTTWarn: 40 * time.Millisecond, RTTCrit: 150 * time.Millisecond,
			JitterWarn: 5 * time.Millisecond, JitterCrit: 25 * time.Millisecond,
			LossWarn: 10, LossCrit: 50,
		},
	}

	got := webMeta(cfg, nil, 2)

	want := web.Meta{
		IntervalMs: 500,
		Features:   web.Features{Traceroute: true, MTR: true, Port: true, HTTP: true, ASN: true, PTR: true, MTU: true},
		Thresholds: web.Thresholds{RTTWarnMs: 40, RTTCritMs: 150, JitterWarnMs: 5, JitterCritMs: 25, LossWarnPct: 10, LossCritPct: 50},
	}
	if got.IntervalMs != want.IntervalMs || got.Features != want.Features || got.Thresholds != want.Thresholds {
		t.Fatalf("webMeta = %+v, want %+v", got, want)
	}
}

func TestWebMetaLeavesOptionalFeaturesOffByDefault(t *testing.T) {
	got := webMeta(config{intervalMs: 1000, thresholds: stats.DefaultThresholds()}, nil, 0)

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
	if got[0].Name != "core" || len(got[0].TargetIDs) != 2 || got[0].TargetIDs[0] != a.ID() || got[0].TargetIDs[1] != c.ID() {
		t.Errorf("core = %+v, want IDs [%d %d]", got[0], a.ID(), c.ID())
	}
	if got[1].Name != "edge" || len(got[1].TargetIDs) != 1 || got[1].TargetIDs[0] != b.ID() {
		t.Errorf("edge = %+v, want only ID %d (out-of-range indices dropped)", got[1], b.ID())
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

func TestCheckWebReloadDrift(t *testing.T) {
	on := func(port int) config { return config{webEnabled: true, webPort: port} }
	off := func(port int) config { return config{webEnabled: false, webPort: port} }
	tests := []struct {
		name             string
		active, reloaded config
		wantWarned       bool
	}{
		{"unchanged on", on(8080), on(8080), false},
		{"unchanged off", off(8080), off(8080), false},
		{"port changed while off", off(8080), off(9090), false},
		{"enabled", off(8080), on(8080), true},
		{"disabled", on(8080), off(8080), true},
		{"port changed while on", on(8080), on(9090), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkWebReloadDrift(tt.active, tt.reloaded)

			if (got != "") != tt.wantWarned {
				t.Fatalf("checkWebReloadDrift = %q, want warned=%v", got, tt.wantWarned)
			}
			if tt.wantWarned && !strings.Contains(got, "restart") {
				t.Errorf("warning %q does not mention a restart", got)
			}
		})
	}
}

// A startup-only web port change must warn without restarting measurements.
func TestRunReloadWithWeb(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(yamlPath, []byte("hosts:\n  - example.com\nweb: true\nweb-port: 18080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var src *web.Source
	var sawWarning bool
	calls := 0
	stubRunSeams(t, func(opts ui.RunOptions) error {
		calls++
		if calls == 1 {
			gotWarning := func() bool {
				for {
					select {
					case line := <-opts.ExternalLogCh:
						if strings.Contains(line, "web: change detected") {
							sawWarning = true
							return true
						}
					default:
						return false
					}
				}
			}
			if !rewriteHostsUntil(t, yamlPath, "hosts:\n  - example.com\nweb: true\nweb-port: 19090\n", gotWarning) {
				t.Error("missing live reload warning")
			}
			return nil
		}
		sawWarning = containsSubstring(opts.InitialLogs, "web: change detected")
		return nil
	})
	webStart = func(opts web.Options) (webServer, error) {
		src = opts.Source
		opts.Port = 0
		return web.Start(opts)
	}
	var statesAtPingerBuild []web.State
	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController {
		if src != nil {
			statesAtPingerBuild = append(statesAtPingerBuild, src.State())
		}
		return &fakePinger{}
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-f", yamlPath, "-S", "127.0.0.1"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if calls != 1 {
		t.Fatalf("UI restarted for metadata change: calls=%d", calls)
	}
	if slices.Contains(statesAtPingerBuild, web.StateReloading) {
		t.Fatal("pinger rebuilt for web port change")
	}
	if !sawWarning {
		t.Error("second iteration's InitialLogs lack the web restart-required warning")
	}
	if got := src.State(); got != web.StateStopped {
		t.Errorf("state after exit = %q, want %q", got, web.StateStopped)
	}
}

// webDo sends an authorised control request the way the dashboard does.
func webDo(t *testing.T, srv webServer, method, path, body string) int {
	t.Helper()
	_, token, ok := strings.Cut(srv.ControlURL(), "#token=")
	if !ok {
		t.Fatalf("ControlURL %q has no token", srv.ControlURL())
	}
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL()+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Mping-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestRunWebControlEditsTheLiveRunAndLogsToTUI(t *testing.T) {
	var started func() webServer
	var statuses []int
	var hostsAfter, logs []string
	var announced []string
	var resetSent int
	started = stubRunSeams(t, func(opts ui.RunOptions) error {
		srv := started()
		announced = opts.InitialLogs
		before := opts.TargetSource().Targets
		before[0].IncSent()
		statuses = append(statuses,
			webDo(t, srv, http.MethodPost, "api/v1/targets", `{"host":"added.example"}`),
			webDo(t, srv, http.MethodDelete, fmt.Sprintf("api/v1/targets/%d", before[1].ID()), ""),
			webDo(t, srv, http.MethodPost, "api/v1/reset", ""),
		)
		resetSent = before[0].GetView().Sent
		for _, ts := range opts.TargetSource().Targets {
			hostsAfter = append(hostsAfter, ts.Host())
		}
		for {
			select {
			case l := <-opts.ExternalLogCh:
				logs = append(logs, l)
				continue
			default:
			}
			break
		}
		return nil
	})
	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController { return newLiveFakePinger() }

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "one.example", "two.example"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusNoContent, http.StatusNoContent}) {
		t.Fatalf("statuses = %v, want [201 204 204]", statuses)
	}
	if !slices.Equal(hostsAfter, []string{"one.example", "added.example"}) {
		t.Errorf("hosts after edits = %v, want [one.example added.example]", hostsAfter)
	}
	if resetSent != 0 {
		t.Errorf("sent after reset = %d, want 0", resetSent)
	}
	for _, want := range []string{"web: added host added.example", "web: deleted two.example", "web: reset statistics"} {
		if !containsSubstring(logs, want) {
			t.Errorf("TUI log lines %q lack %q", logs, want)
		}
	}
	if !containsSubstring(announced, started().ControlURL()) {
		t.Errorf("initial logs %q do not offer the control URL", announced)
	}
}

func TestWebLogLinesEscapeTviewTags(t *testing.T) {
	logCh := make(chan string, 1)
	c := webController{logCh: logCh}

	c.logf("added host %s", "[red]evil")

	if got := <-logCh; strings.Contains(got, "[red]evil") {
		t.Fatalf("log line %q passes a host's [tag] through to tview", got)
	}
}

func TestWebLogNeverBlocksWhenTUILogIsFull(t *testing.T) {
	c := webController{logCh: make(chan string)} // unbuffered, nobody reading

	done := make(chan struct{})
	go func() {
		c.logf("reset statistics")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("logf blocked on a full TUI log channel")
	}
}

// newStopProbePinger returns a pinger that runs onStop when the supervisor
// tears measurements down, which only happens after uiRun has returned
// (finishIteration).
func newStopProbePinger(onStop func()) *liveFakePinger {
	p := newLiveFakePinger()
	p.onStop = onStop
	return p
}

// TestRunWebStopsAcceptingEditsOnceTheUIIterationEnds covers the window
// between uiRun returning and the run loop deciding reload-or-exit: the
// host list has already been captured and the supervisor is being torn
// down, so a browser edit accepted there would be acknowledged and lost.
func TestRunWebStopsAcceptingEditsOnceTheUIIterationEnds(t *testing.T) {
	var src *web.Source
	var atTeardown []web.State
	stubRunSeams(t, func(ui.RunOptions) error { return nil })
	webStart = func(opts web.Options) (webServer, error) {
		src = opts.Source
		opts.Port = 0
		return web.Start(opts)
	}
	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController {
		return newStopProbePinger(func() {
			if src != nil {
				atTeardown = append(atTeardown, src.State())
			}
		})
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if len(atTeardown) == 0 {
		t.Fatal("pinger Stop never ran during teardown")
	}
	if got := atTeardown[len(atTeardown)-1]; got == web.StateRunning {
		t.Fatalf("web state during teardown = %q; edits would still be accepted", got)
	}
	if got := src.State(); got != web.StateStopped {
		t.Errorf("final state = %q, want %q", got, web.StateStopped)
	}
}

func TestWebControllerReportsRejectionsWithoutLogging(t *testing.T) {
	only := stats.NewTargetStats("only.example")
	sup := newSupervisor(supervisorConfig{
		targets: []*stats.TargetStats{only},
		specs:   []targetSpec{{Host: "only.example"}},
	})
	sup.Start()
	defer sup.Shutdown()
	logCh := make(chan string, 4)
	c := webController{sup: sup, logCh: logCh}

	addErr := c.AddHost("only.example")
	delErr := c.DeleteTarget(only.ID())

	if addErr == nil || !strings.Contains(addErr.Error(), "already in the list") {
		t.Errorf("AddHost duplicate err = %v, want already-in-the-list", addErr)
	}
	if delErr == nil || !strings.Contains(delErr.Error(), "last host") {
		t.Errorf("DeleteTarget last err = %v, want last-host refusal", delErr)
	}
	if len(logCh) != 0 {
		t.Errorf("rejected edits logged %d line(s) to the TUI", len(logCh))
	}
}

// The announcement sits among timestamped Log lines and must look like them.
func TestWebAnnouncementIsATimestampedLogLine(t *testing.T) {
	srv, err := web.Start(web.Options{Port: 0, Source: web.NewSource()})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	want := regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\] Web UI: http://localhost:\d+/`)
	line := webAnnouncement(srv)
	if got := plainLogLine(line); !want.MatchString(got) {
		t.Errorf("rendered line = %q, want %v", got, want)
	}
	if !strings.Contains(line, "#token=") {
		t.Errorf("line %q lacks the control link", line)
	}
}

func TestRunUsesMPINGWEBTOKENForTheWebServer(t *testing.T) {
	token := strings.Repeat("t", 32)
	t.Setenv("MPING_WEB_TOKEN", token)
	stubRunSeams(t, func(ui.RunOptions) error { return nil })
	var got string
	webStart = func(opts web.Options) (webServer, error) {
		got = opts.Token
		opts.Port = 0
		return web.Start(opts)
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if got != token {
		t.Fatalf("web token = %q, want MPING_WEB_TOKEN", got)
	}
}

func TestRunRejectsAWeakMPINGWEBTOKENWithoutEchoingIt(t *testing.T) {
	t.Setenv("MPING_WEB_TOKEN", "short")
	stubRunSeams(t, func(ui.RunOptions) error {
		t.Error("UI started with an invalid token")
		return nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "MPING_WEB_TOKEN") || strings.Contains(errOut.String(), "short") {
		t.Fatalf("stderr = %q, want an MPING_WEB_TOKEN error that does not echo the value", errOut.String())
	}
}
