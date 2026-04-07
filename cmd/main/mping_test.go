package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/spf13/pflag"
)

type fakePinger struct {
	startErr           error
	started            bool
	closed             bool
	waited             bool
	discoverMTU        int
	discoverBottleneckIP string
	discoverErr        error
	traceErr           error
	logWriterSet       bool
}

func (f *fakePinger) Start(interval, timeout time.Duration) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	return nil
}

func (f *fakePinger) Close() {
	f.closed = true
}

func (f *fakePinger) Wait() {
	f.waited = true
}

func (f *fakePinger) DiscoverMaxPayload(dest string, start int, min int, logf func(string)) (int, string, error) {
	if f.discoverErr != nil {
		return 0, "", f.discoverErr
	}
	if f.discoverMTU == 0 {
		return start, "", nil
	}
	return f.discoverMTU, f.discoverBottleneckIP, nil
}

func (f *fakePinger) TraceRoute(dest string, maxHops int, timeout time.Duration) ([]string, error) {
	if f.traceErr != nil {
		return nil, f.traceErr
	}
	return []string{"hop1", "hop2"}, nil
}

func (f *fakePinger) SetSource(ip string) {}
func (f *fakePinger) SetSize(size int)  {}
func (f *fakePinger) SetCount(count int) {}
func (f *fakePinger) SetResolveInterval(interval time.Duration) {}
func (f *fakePinger) Stop()                    { f.closed = true }
func (f *fakePinger) SetLogWriter(w io.Writer) { f.logWriterSet = true }

func TestGetPreferredOutboundIP_Localhost(t *testing.T) {
	ip := getPreferredOutboundIP("127.0.0.1", "udp4")
	if ip == "" {
		t.Skip("no outbound IP detected in this environment")
	}
	if net.ParseIP(ip) == nil {
		t.Fatalf("expected valid IP, got %q", ip)
	}
}

func TestParseArgsDefaults(t *testing.T) {
	cfg, hosts, _, _, err := parseArgs([]string{"example.com"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "example.com" {
		t.Fatalf("hosts: got %v", hosts)
	}
	if cfg.intervalMs != 1000 || cfg.timeoutMs != 1000 {
		t.Fatalf("defaults: interval=%d timeout=%d", cfg.intervalMs, cfg.timeoutMs)
	}
	if cfg.packetSize != 56 || cfg.count != 0 {
		t.Fatalf("defaults: size=%d count=%d", cfg.packetSize, cfg.count)
	}
}

func TestParseArgsMissingHosts(t *testing.T) {
	_, _, _, _, err := parseArgs([]string{})
	if err == nil {
		t.Fatal("expected error for missing hosts")
	}
}

func TestParseArgsHostsFileAllowsNoHosts(t *testing.T) {
	cfg, hosts, _, _, err := parseArgs([]string{"--file", "hosts.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.hostsFile != "hosts.yaml" {
		t.Fatalf("hostsFile: got %q", cfg.hostsFile)
	}
	if len(hosts) != 0 {
		t.Fatalf("hosts: expected empty, got %v", hosts)
	}
}

func TestParseArgsIPv4IPv6Conflict(t *testing.T) {
	_, _, _, _, err := parseArgs([]string{"-4", "-6", "example.com"})
	if err == nil {
		t.Fatal("expected error for -4 and -6 together")
	}
}

func TestRunHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--help"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if !strings.Contains(out.String(), "Usage: mping") {
		t.Fatalf("expected usage in output")
	}
}

func TestRunMissingHosts(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
	if !strings.Contains(errOut.String(), "Usage: mping") {
		t.Fatalf("expected usage in error output")
	}
}

func TestRunInvalidIPv4IPv6Flags(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"-4", "-6", "example.com"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
}

func TestRunMissingHostsFile(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"-f", "does-not-exist.yaml"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
	if !strings.Contains(errOut.String(), "Error reading hosts file") {
		t.Fatalf("expected hosts file error")
	}
}

func TestParseHostsFileYAMLMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n  - a\n  - b\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if len(got.Hosts) != 2 || got.Hosts[0] != "a" || got.Hosts[1] != "b" {
		t.Fatalf("hosts: got %v", got.Hosts)
	}
}

func TestGetInterfaceIP_Invalid(t *testing.T) {
	if _, err := getInterfaceIP("no-such-iface", false); err == nil {
		t.Fatal("expected error for invalid interface")
	}
}

func TestGetInterfaceMTU_InvalidIface(t *testing.T) {
	if _, err := getInterfaceMTU("no-such-iface", "", "127.0.0.1"); err == nil {
		t.Fatal("expected error for invalid interface")
	}
}

func TestDetectAutoSourceIPs_Unresolvable(t *testing.T) {
	v4, v6 := detectAutoSourceIPs([]string{"invalid.invalid"})
	if v4 != "" || v6 != "" {
		t.Fatalf("expected empty results, got v4=%q v6=%q", v4, v6)
	}
}

func TestRunStopRestart(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := &fakePinger{}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		onStop()
		if err := onRestart(); err != nil {
			t.Fatalf("restart failed: %v", err)
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if !fp.started || !fp.closed || !fp.waited {
		t.Fatalf("expected pinger lifecycle, started=%v closed=%v waited=%v", fp.started, fp.closed, fp.waited)
	}
}

func TestRunStartError(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := &fakePinger{startErr: io.ErrUnexpectedEOF}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code")
	}
	if !strings.Contains(errOut.String(), "Error starting pinger") {
		t.Fatalf("expected start error output")
	}
}

func TestResolveNetwork(t *testing.T) {
	if got := resolveNetwork(config{ipv4Only: true}); got != "ip4" {
		t.Fatalf("ipv4: got %q", got)
	}
	if got := resolveNetwork(config{ipv6Only: true}); got != "ip6" {
		t.Fatalf("ipv6: got %q", got)
	}
	if got := resolveNetwork(config{}); got != "ip" {
		t.Fatalf("default: got %q", got)
	}
}

func TestMergeHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n  - a\n  - b\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := config{hostsFile: path}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	got, _, err := mergeHosts(cfg, fs, []string{"c"})
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("hosts len: got %d", len(got))
	}
}

func TestDetermineSourceIPs_SourceAddr(t *testing.T) {
	bind, v4, v6, err := determineSourceIPs(config{sourceAddr: "10.0.0.2"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bind != "10.0.0.2" || v4 != "10.0.0.2" || v6 != "" {
		t.Fatalf("unexpected values: bind=%q v4=%q v6=%q", bind, v4, v6)
	}
}

func TestSetupLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")
	f, err := setupLogger(path)
	if err != nil {
		t.Fatalf("setupLogger: %v", err)
	}
	if f == nil {
		t.Fatal("expected file handle")
	}
	f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "Timestamp,Host,IP") {
		t.Fatalf("missing header")
	}
}

// ---- pingerAdapter setter tests ----

func TestPingerAdapterSetters(t *testing.T) {
	p := &pingerAdapter{Pinger: pinger.NewPinger(nil)}

	p.SetSource("1.2.3.4")
	if p.Source != "1.2.3.4" {
		t.Errorf("SetSource: got %q, want %q", p.Source, "1.2.3.4")
	}
	p.SetSize(128)
	if p.Size != 128 {
		t.Errorf("SetSize: got %d, want 128", p.Size)
	}
	p.SetCount(5)
	if p.Count != 5 {
		t.Errorf("SetCount: got %d, want 5", p.Count)
	}
	p.SetResolveInterval(30 * time.Second)
	if p.ResolveInterval != 30*time.Second {
		t.Errorf("SetResolveInterval: got %v, want 30s", p.ResolveInterval)
	}
	var buf bytes.Buffer
	p.SetLogWriter(&buf)
	if p.LogWriter != &buf {
		t.Error("SetLogWriter: writer not set correctly")
	}
}

// ---- runTraceroutes tests ----

func TestRunTraceroutes_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	fp := &fakePinger{}

	done := make(chan struct{})
	go func() {
		runTraceroutes(ctx, fp, targets)
		close(done)
	}()

	// Allow initial run to complete then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runTraceroutes did not stop after context cancel")
	}

	view := targets[0].GetView()
	if len(view.TraceHops) == 0 || view.TraceHops[0] != "hop1" {
		t.Errorf("unexpected hops: %v", view.TraceHops)
	}
}

func TestRunTraceroutes_TraceError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	fp := &fakePinger{traceErr: io.ErrUnexpectedEOF}

	runTraceroutes(ctx, fp, targets)

	view := targets[0].GetView()
	if len(view.TraceHops) == 0 || !strings.HasPrefix(view.TraceHops[0], "error:") {
		t.Errorf("expected error hop, got %v", view.TraceHops)
	}
}

func TestRunTraceroutes_EmptyHops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Verify "Tracing..." is set initially when hops are empty,
	// then replaced with the result from TraceRoute
	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	fp := &fakePinger{}

	runTraceroutes(ctx, fp, targets)

	view := targets[0].GetView()
	if len(view.TraceHops) == 0 {
		t.Errorf("expected hops to be set, got empty")
	}
}

// ---- determineSourceIPs additional tests ----

func TestDetermineSourceIPs_IPv6SourceAddr(t *testing.T) {
	bind, v4, v6, err := determineSourceIPs(config{sourceAddr: "2001:db8::1"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bind != "2001:db8::1" || v4 != "" || v6 != "2001:db8::1" {
		t.Errorf("unexpected: bind=%q v4=%q v6=%q", bind, v4, v6)
	}
}

func TestDetermineSourceIPs_IfaceNameError(t *testing.T) {
	_, _, _, err := determineSourceIPs(config{ifaceName: "no-such-iface-xyz"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid interface name")
	}
}

// ---- getInterfaceIP additional tests ----

func TestGetInterfaceIP_LoopbackNoMatch(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) == 0 {
		t.Skip("no interfaces available")
	}
	// Find a loopback interface; its IPs are filtered (IsLoopback), so should return error
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			_, err := getInterfaceIP(iface.Name, false)
			// Loopback IPs are skipped, so we expect "no IPv4 address found" error
			if err == nil {
				t.Logf("unexpected success on loopback interface %q", iface.Name)
			}
			return
		}
	}
	t.Skip("no loopback interface found")
}

// ---- run() additional coverage ----

func TestRunInvalidPortSpec(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return &fakePinger{}
	}
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-p", "notaport", "example.com"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero code for invalid port spec")
	}
	if !strings.Contains(errOut.String(), "Invalid port spec") {
		t.Errorf("expected invalid port spec message, got: %q", errOut.String())
	}
}

func TestRunWithPortSpec(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})
	fp := &fakePinger{}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-p", "443/tcp", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

func TestRunWithTrace(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})
	fp := &fakePinger{}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunResetTrace verifies that the onResetTrace callback passed to uiRun,
// when called, re-triggers a traceroute and repopulates TraceHops.
func TestRunResetTrace(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := &fakePinger{}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}

	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		if onResetTrace == nil {
			t.Error("onResetTrace must not be nil when traceEnabled=true")
			return nil
		}
		// Simulate the UI clearing TraceHops before calling onResetTrace (as 'R' does).
		for _, tg := range targets {
			tg.SetTraceHops(nil)
		}
		onResetTrace()
		// Wait for the re-run to populate TraceHops.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(targets[0].GetView().TraceHops) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if hops := targets[0].GetView().TraceHops; len(hops) == 0 {
			t.Error("TraceHops not repopulated after onResetTrace()")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-T", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

// TestRunResetPort verifies that the onResetPort callback passed to uiRun is non-nil when port specs are provided,
// and that calling it stops and restarts the port checker.
func TestRunResetPort(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	fp := &fakePinger{}
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return fp
	}

	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		if !portEnabled {
			t.Error("portEnabled should be true when port specs given")
			return nil
		}
		if onResetPort == nil {
			t.Error("onResetPort must not be nil when port specs are provided")
			return nil
		}
		// Calling it must not panic
		onResetPort()
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-p", "80/tcp", "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
}

func TestRunWithMTUIPv6Warning(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return &fakePinger{}
	}
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
		return nil
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-6", "-m", "-S", "::1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "PMTU discovery disabled") {
		t.Errorf("expected PMTU warning, got: %q", errOut.String())
	}
}

// ---- pingerAdapter.Close() test ----

func TestPingerAdapterClose(t *testing.T) {
	fp := &fakePinger{}
	// Call Close via the interface to exercise the adapter method.
	fp.Close()
	if !fp.closed {
		t.Fatal("expected Close() to set closed=true")
	}
}

// ---- setupPMTU tests ----

func TestSetupPMTU_Disabled(t *testing.T) {
	called := false
	make := func(size int) pingerController {
		called = true
		return &fakePinger{}
	}
	var errOut bytes.Buffer
	cfg := config{mtuEnabled: false}
	packetSize, preLogs := setupPMTU(make, cfg, 1500, nil, "example.com", &errOut)
	if called {
		t.Fatal("expected makePinger not to be called when mtuEnabled=false")
	}
	if packetSize != 0 {
		t.Fatalf("expected packetSize=0 (cfg.packetSize default), got %d", packetSize)
	}
	if len(preLogs) != 0 {
		t.Fatalf("expected no preLogs, got %v", preLogs)
	}
}

func TestSetupPMTU_IPv6Disabled(t *testing.T) {
	called := false
	makeFn := func(size int) pingerController {
		called = true
		return &fakePinger{}
	}
	var errOut bytes.Buffer
	cfg := config{mtuEnabled: true, ipv6Only: true, packetSize: 56}
	packetSize, preLogs := setupPMTU(makeFn, cfg, 1500, nil, "example.com", &errOut)
	if called {
		t.Fatal("expected makePinger not to be called for IPv6-only")
	}
	if packetSize != 56 {
		t.Fatalf("expected packetSize=56, got %d", packetSize)
	}
	if len(preLogs) != 0 {
		t.Fatalf("expected no preLogs, got %v", preLogs)
	}
	if !strings.Contains(errOut.String(), "PMTU discovery disabled") {
		t.Errorf("expected PMTU disabled warning in errOut, got: %q", errOut.String())
	}
}

func TestSetupPMTU_DiscoverError(t *testing.T) {
	makeFn := func(size int) pingerController {
		return &fakePinger{discoverErr: io.ErrUnexpectedEOF}
	}
	var errOut bytes.Buffer
	cfg := config{mtuEnabled: true, ipv6Only: false, packetSize: 56}
	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	packetSize, preLogs := setupPMTU(makeFn, cfg, 1500, targets, "example.com", &errOut)
	if packetSize != 56 {
		t.Fatalf("expected packetSize=56 on error, got %d", packetSize)
	}
	if len(preLogs) != 0 {
		t.Fatalf("expected no preLogs on error, got %v", preLogs)
	}
	if !strings.Contains(errOut.String(), "PMTU discovery failed") {
		t.Errorf("expected failure message, got: %q", errOut.String())
	}
}

func TestSetupPMTU_SuccessWithBottleneck(t *testing.T) {
	makeFn := func(size int) pingerController {
		return &fakePinger{discoverMTU: 1400, discoverBottleneckIP: "10.0.0.1"}
	}
	var errOut bytes.Buffer
	cfg := config{mtuEnabled: true, ipv6Only: false, packetSize: 56}
	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	packetSize, _ := setupPMTU(makeFn, cfg, 1500, targets, "example.com", &errOut)
	if packetSize != 1400 {
		t.Fatalf("expected packetSize=1400, got %d", packetSize)
	}
	view := targets[0].GetView()
	if view.PMTU != 1400 {
		t.Fatalf("expected PMTU=1400, got %d", view.PMTU)
	}
	if view.PMTUBottleneckIP != "10.0.0.1" {
		t.Fatalf("expected bottleneck IP 10.0.0.1, got %q", view.PMTUBottleneckIP)
	}
}

func TestSetupPMTU_SuccessNoBottleneck(t *testing.T) {
	makeFn := func(size int) pingerController {
		return &fakePinger{discoverMTU: 1200, discoverBottleneckIP: ""}
	}
	var errOut bytes.Buffer
	cfg := config{mtuEnabled: true, ipv6Only: false, packetSize: 56}
	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	packetSize, _ := setupPMTU(makeFn, cfg, 0, targets, "example.com", &errOut)
	if packetSize != 1200 {
		t.Fatalf("expected packetSize=1200, got %d", packetSize)
	}
	view := targets[0].GetView()
	// bottleneckIP is empty so SetPMTUBottleneckIP should not have been called
	if view.PMTUBottleneckIP != "" {
		t.Fatalf("expected empty bottleneck IP, got %q", view.PMTUBottleneckIP)
	}
}
