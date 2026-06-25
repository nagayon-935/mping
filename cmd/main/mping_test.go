package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
)

type fakePinger struct {
	startErr             error
	started              bool
	closed               bool
	waited               bool
	discoverMTU          int
	discoverBottleneckIP string
	discoverErr          error
	traceErr             error
	logWriterSet         bool
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

func (f *fakePinger) SetSource(ip string)                       {}
func (f *fakePinger) SetSize(size int)                          {}
func (f *fakePinger) SetCount(count int)                        {}
func (f *fakePinger) SetResolveInterval(interval time.Duration) {}
func (f *fakePinger) Stop()                                     { f.closed = true }
func (f *fakePinger) SetLogWriter(w io.Writer)                  { f.logWriterSet = true }

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
	uiRun = func(opts ui.RunOptions) error {
		opts.OnStop()
		if err := opts.OnRestart(); err != nil {
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
	uiRun = func(opts ui.RunOptions) error {
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
	got, _, _, err := mergeHosts(cfg, fs, []string{"c"})
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("hosts len: got %d", len(got))
	}
}

func TestMergeHosts_Options(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	yamlContent := `
hosts:
  - example.com
interval: 2000
timeout: 3000
output: log.csv
interface: eth0
source: 10.0.0.1
size: 100
count: 5
discovery-mtu: true
traceroute: true
asn: true
ipv4: true
port:
  - 80/tcp
`
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config{hostsFile: path}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int("interval", 1000, "")
	fs.Int("timeout", 1000, "")
	fs.String("output", "", "")
	fs.String("interface", "", "")
	fs.String("source", "", "")
	fs.Int("size", 56, "")
	fs.Int("count", 0, "")
	fs.Bool("discovery-mtu", false, "")
	fs.Bool("traceroute", false, "")
	fs.Bool("asn", false, "")
	fs.Bool("ipv4", false, "")
	fs.StringSlice("port", nil, "")

	gotHosts, _, gotCfg, err := mergeHosts(cfg, fs, nil)
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}

	if len(gotHosts) != 1 || gotHosts[0] != "example.com" {
		t.Errorf("hosts: got %v", gotHosts)
	}
	if gotCfg.intervalMs != 2000 {
		t.Errorf("intervalMs: got %d", gotCfg.intervalMs)
	}
	if gotCfg.timeoutMs != 3000 {
		t.Errorf("timeoutMs: got %d", gotCfg.timeoutMs)
	}
	if gotCfg.outputFile != "log.csv" {
		t.Errorf("outputFile: got %q", gotCfg.outputFile)
	}
	if gotCfg.ifaceName != "eth0" {
		t.Errorf("ifaceName: got %q", gotCfg.ifaceName)
	}
	if gotCfg.sourceAddr != "10.0.0.1" {
		t.Errorf("sourceAddr: got %q", gotCfg.sourceAddr)
	}
	if gotCfg.packetSize != 100 {
		t.Errorf("packetSize: got %d", gotCfg.packetSize)
	}
	if gotCfg.count != 5 {
		t.Errorf("count: got %d", gotCfg.count)
	}
	if gotCfg.mtuEnabled != true {
		t.Errorf("mtuEnabled: got %v", gotCfg.mtuEnabled)
	}
	if gotCfg.trace != true {
		t.Errorf("trace: got %v", gotCfg.trace)
	}
	if gotCfg.asnEnabled != true {
		t.Errorf("asnEnabled: got %v", gotCfg.asnEnabled)
	}
	if gotCfg.ipv4Only != true {
		t.Errorf("ipv4Only: got %v", gotCfg.ipv4Only)
	}
	if len(gotCfg.portSpecs) != 1 || gotCfg.portSpecs[0] != "80/tcp" {
		t.Errorf("portSpecs: got %v", gotCfg.portSpecs)
	}

	// Test command line override
	fs.Set("interval", "5000")
	cfg.intervalMs = 5000 // simulate parseArgs setting cfg from fs
	_, _, gotCfg2, _ := mergeHosts(cfg, fs, nil)
	if gotCfg2.intervalMs != 5000 {
		t.Errorf("intervalMs override: got %d, want 5000", gotCfg2.intervalMs)
	}
}

func TestMergeHosts_IPv4AndIPv6Conflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n  - a\nipv4: true\nipv6: true\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := config{hostsFile: path}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	_, _, _, err := mergeHosts(cfg, fs, nil)
	if err == nil {
		t.Fatal("expected error for ipv4+ipv6 conflict, got nil")
	}
	if !strings.Contains(err.Error(), "cannot use both -4 and -6") {
		t.Errorf("unexpected error message: %v", err)
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

func TestSetupLogger_ExistingFile_NoExtraHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")

	// First run: creates file and writes header.
	f, err := setupLogger(path)
	if err != nil {
		t.Fatalf("first setupLogger: %v", err)
	}
	f.Close()

	// Second run: file is non-empty; header must NOT be appended again.
	f2, err := setupLogger(path)
	if err != nil {
		t.Fatalf("second setupLogger: %v", err)
	}
	f2.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	const header = "Timestamp,Host,IP,Seq,Status,RTT(ms),TTL,Error\n"
	if count := strings.Count(string(data), header); count != 1 {
		t.Errorf("header appears %d times, want exactly 1", count)
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
	uiRun = func(opts ui.RunOptions) error {
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
	uiRun = func(opts ui.RunOptions) error {
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
	uiRun = func(opts ui.RunOptions) error {
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

	uiRun = func(opts ui.RunOptions) error {
		if opts.OnResetTrace == nil {
			t.Error("OnResetTrace must not be nil when TraceEnabled=true")
			return nil
		}
		// Simulate the UI clearing TraceHops before calling OnResetTrace (as 'R' does).
		for _, tg := range opts.Targets {
			tg.SetTraceHops(nil)
		}
		opts.OnResetTrace()
		// Wait for the re-run to populate TraceHops.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(opts.Targets[0].GetView().TraceHops) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if hops := opts.Targets[0].GetView().TraceHops; len(hops) == 0 {
			t.Error("TraceHops not repopulated after OnResetTrace()")
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

	uiRun = func(opts ui.RunOptions) error {
		if !opts.PortEnabled {
			t.Error("PortEnabled should be true when port specs given")
			return nil
		}
		if opts.OnResetPort == nil {
			t.Error("OnResetPort must not be nil when port specs are provided")
			return nil
		}
		// Calling it must not panic
		opts.OnResetPort()
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
	uiRun = func(opts ui.RunOptions) error {
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

func TestDetermineSourceIPs_Interface(t *testing.T) {
	oldInterfaceByName := interfaceByName
	defer func() { interfaceByName = oldInterfaceByName }()

	interfaceByName = func(name string) (*net.Interface, error) {
		return nil, fmt.Errorf("mock error")
	}

	cfg := config{ifaceName: "eth0"}
	_, _, _, err := determineSourceIPs(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing interface, got nil")
	}
}

func TestDetermineSourceIPs_Auto(t *testing.T) {
	// detectAutoSourceIPs might return empty strings if no network, but it shouldn't fail.
	_, v4, v6, err := determineSourceIPs(config{}, []string{"8.8.8.8"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("Auto IPs: v4=%q, v6=%q", v4, v6)
}

func TestInitTargets(t *testing.T) {
	hosts := []string{"a", "b"}
	targets := initTargets(hosts)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].Host != "a" || targets[1].Host != "b" {
		t.Errorf("targets host mismatch")
	}
}

// ---- validateHostsDoc tests ----

func TestValidateHostsDoc_Valid(t *testing.T) {
	ms := func(v int) *int { return &v }
	doc := hostsFileYAML{
		Hosts:      []string{"a.com", "b.com"},
		IntervalMs: ms(500),
		TimeoutMs:  ms(1000),
		PacketSize: ms(56),
		Count:      ms(0),
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Errorf("expected valid doc, got: %v", err)
	}
}

func TestValidateHostsDoc_EmptyHosts(t *testing.T) {
	doc := hostsFileYAML{Hosts: []string{}}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for empty hosts")
	}
}

func TestValidateHostsDoc_NilHosts(t *testing.T) {
	if err := validateHostsDoc(hostsFileYAML{}); err == nil {
		t.Fatal("expected error for nil hosts")
	}
}

func TestValidateHostsDoc_EmptyHostEntry(t *testing.T) {
	doc := hostsFileYAML{Hosts: []string{"a.com", "  "}}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for blank host entry")
	}
}

func TestValidateHostsDoc_InvalidInterval(t *testing.T) {
	v := 50 // below minimum 100
	doc := hostsFileYAML{Hosts: []string{"a.com"}, IntervalMs: &v}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for interval=50")
	}
	v = 99999 // above maximum
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for interval=99999")
	}
}

func TestValidateHostsDoc_InvalidTimeout(t *testing.T) {
	v := 5 // below minimum 10
	doc := hostsFileYAML{Hosts: []string{"a.com"}, TimeoutMs: &v}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for timeout=5")
	}
}

func TestValidateHostsDoc_InvalidSize(t *testing.T) {
	v := 0 // below minimum 1
	doc := hostsFileYAML{Hosts: []string{"a.com"}, PacketSize: &v}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for size=0")
	}
}

func TestValidateHostsDoc_NegativeCount(t *testing.T) {
	v := -1
	doc := hostsFileYAML{Hosts: []string{"a.com"}, Count: &v}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for count=-1")
	}
}

func TestValidateHostsDoc_IPv4IPv6Conflict(t *testing.T) {
	tr := true
	doc := hostsFileYAML{Hosts: []string{"a.com"}, Ipv4Only: &tr, Ipv6Only: &tr}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for ipv4+ipv6 both true")
	}
}

// ---- parseArgs -j/--json-output flag tests ----

func TestParseArgs_JSONOutputShortFlag(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"-j", "/tmp/stats.json", "example.com"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.jsonOutputFile != "/tmp/stats.json" {
		t.Errorf("jsonOutputFile = %q, want %q", cfg.jsonOutputFile, "/tmp/stats.json")
	}
}

func TestParseArgs_JSONOutputLongFlag(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"--json-output", "/tmp/out.json", "example.com"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.jsonOutputFile != "/tmp/out.json" {
		t.Errorf("jsonOutputFile = %q, want %q", cfg.jsonOutputFile, "/tmp/out.json")
	}
}

// ---- writeJSONSnapshot tests ----

func TestWriteJSONSnapshot_CreatesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")

	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	targets[0].SetIP("1.2.3.4")

	if err := writeJSONSnapshot(path, targets, nil); err != nil {
		t.Fatalf("writeJSONSnapshot: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := out["timestamp"]; !ok {
		t.Error("missing 'timestamp' in JSON")
	}
	if _, ok := out["targets"]; !ok {
		t.Error("missing 'targets' in JSON")
	}
}

func TestWriteJSONSnapshot_Atomic(t *testing.T) {
	// Verify that the .tmp file is cleaned up and only the final file exists.
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")

	if err := writeJSONSnapshot(path, nil, nil); err != nil {
		t.Fatalf("writeJSONSnapshot: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("expected .tmp file to be removed after rename")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected final file to exist: %v", err)
	}
}

// ---- run() with -j flag ----

// TestRunJSONOutput_FinalWrite verifies that a valid JSON snapshot file is
// written when the TUI exits (final snapshot path, no ticker dependency).
func TestRunJSONOutput_FinalWrite(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return &fakePinger{}
	}

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "stats.json")

	// Return immediately to trigger only the final snapshot write.
	uiRun = func(opts ui.RunOptions) error { return nil }

	var out, errOut bytes.Buffer
	code := run([]string{"-j", jsonPath, "-S", "127.0.0.1", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("JSON file not created: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := snap["timestamp"]; !ok {
		t.Error("missing 'timestamp' in JSON output")
	}
	if _, ok := snap["targets"]; !ok {
		t.Error("missing 'targets' in JSON output")
	}
}

// ---- YAML auto-reload integration test ----

func TestRunYAMLReload(t *testing.T) {
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

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(yamlPath, []byte("hosts:\n  - example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	callCount := 0
	uiRun = func(opts ui.RunOptions) error {
		callCount++
		if callCount == 1 {
			// Write a new hosts file while the TUI is "running".
			// The watcher should detect this and close ExternalCloseCh.
			time.Sleep(100 * time.Millisecond)
			if err := os.WriteFile(yamlPath,
				[]byte("hosts:\n  - example.com\n  - 1.1.1.1\n"), 0644); err != nil {
				t.Errorf("write yaml: %v", err)
			}
			// Wait for watcher debounce + processing.
			time.Sleep(600 * time.Millisecond)
		}
		// Return nil (simulates user quit or ExternalCloseCh-driven stop).
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-f", yamlPath, "-S", "127.0.0.1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	// uiRun should have been called twice: once for initial, once after reload.
	if callCount < 2 {
		t.Errorf("expected uiRun to be called >=2 times (reload), got %d", callCount)
	}
}

// ---- threshold configuration tests ----

func TestParseArgs_ThresholdDefaults(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"example.com"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.rttWarnMs != 50 || cfg.rttCritMs != 200 {
		t.Errorf("rtt defaults: got warn=%d crit=%d, want 50/200", cfg.rttWarnMs, cfg.rttCritMs)
	}
	if cfg.jitterWarnMs != 10 || cfg.jitterCritMs != 50 {
		t.Errorf("jitter defaults: got warn=%d crit=%d, want 10/50", cfg.jitterWarnMs, cfg.jitterCritMs)
	}
	if cfg.lossWarnPct != 20 || cfg.lossCritPct != 80 {
		t.Errorf("loss defaults: got warn=%g crit=%g, want 20/80", cfg.lossWarnPct, cfg.lossCritPct)
	}
	if err := thresholdsFromCfg(cfg).Validate(); err != nil {
		t.Errorf("default thresholds should validate: %v", err)
	}
}

func TestParseArgs_ThresholdFlags(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{
		"--rtt-warn", "30", "--rtt-crit", "100",
		"--jitter-warn", "5", "--jitter-crit", "20",
		"--loss-warn", "10", "--loss-crit", "50",
		"example.com",
	})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	th := thresholdsFromCfg(cfg)
	if th.RTTWarn != 30*time.Millisecond || th.RTTCrit != 100*time.Millisecond {
		t.Errorf("rtt: got %v/%v, want 30ms/100ms", th.RTTWarn, th.RTTCrit)
	}
	if th.LossWarn != 10 || th.LossCrit != 50 {
		t.Errorf("loss: got %g/%g, want 10/50", th.LossWarn, th.LossCrit)
	}
	if err := th.Validate(); err != nil {
		t.Errorf("custom thresholds should validate: %v", err)
	}
}

func TestApplyThresholdsDoc_YAMLApplied(t *testing.T) {
	// No threshold flags set → YAML values apply.
	cfg, _, fs, _, err := parseArgs([]string{"example.com"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	mi := func(v int) *int { return &v }
	mf := func(v float64) *float64 { return &v }
	applyThresholdsDoc(&cfg, fs, &thresholdsYAML{
		RTTWarn: mi(25), RTTCrit: mi(90),
		LossWarn: mf(15), LossCrit: mf(60),
	})
	if cfg.rttWarnMs != 25 || cfg.rttCritMs != 90 {
		t.Errorf("rtt from YAML: got %d/%d, want 25/90", cfg.rttWarnMs, cfg.rttCritMs)
	}
	if cfg.lossWarnPct != 15 || cfg.lossCritPct != 60 {
		t.Errorf("loss from YAML: got %g/%g, want 15/60", cfg.lossWarnPct, cfg.lossCritPct)
	}
	// Untouched fields keep flag defaults.
	if cfg.jitterWarnMs != 10 || cfg.jitterCritMs != 50 {
		t.Errorf("jitter should keep defaults: got %d/%d", cfg.jitterWarnMs, cfg.jitterCritMs)
	}
}

func TestApplyThresholdsDoc_FlagOverridesYAML(t *testing.T) {
	// rtt-warn set on CLI → YAML rtt-warn ignored, others apply.
	cfg, _, fs, _, err := parseArgs([]string{"--rtt-warn", "40", "example.com"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	mi := func(v int) *int { return &v }
	applyThresholdsDoc(&cfg, fs, &thresholdsYAML{
		RTTWarn: mi(25), RTTCrit: mi(90),
	})
	if cfg.rttWarnMs != 40 {
		t.Errorf("CLI rtt-warn should win: got %d, want 40", cfg.rttWarnMs)
	}
	if cfg.rttCritMs != 90 {
		t.Errorf("YAML rtt-crit should apply: got %d, want 90", cfg.rttCritMs)
	}
}

func TestValidateHostsDoc_ThresholdsInvalid(t *testing.T) {
	mi := func(v int) *int { return &v }
	doc := hostsFileYAML{
		Hosts: []string{"a.com"},
		Thresholds: &thresholdsYAML{
			RTTWarn: mi(200), RTTCrit: mi(100), // warn >= crit
		},
	}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for rtt-warn >= rtt-crit")
	}
}

func TestValidateHostsDoc_ThresholdsValid(t *testing.T) {
	mi := func(v int) *int { return &v }
	mf := func(v float64) *float64 { return &v }
	doc := hostsFileYAML{
		Hosts: []string{"a.com"},
		Thresholds: &thresholdsYAML{
			RTTWarn: mi(30), RTTCrit: mi(120),
			LossWarn: mf(10), LossCrit: mf(70),
		},
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Errorf("expected valid thresholds, got: %v", err)
	}
}

// ── Phase 5: YAML groups ──────────────────────────────────────────────────────

func TestValidateHostsDoc_GroupsOnly(t *testing.T) {
	// groups: alone (no hosts:) should be valid
	doc := hostsFileYAML{
		Groups: []groupYAML{
			{Name: "DC1", Hosts: []string{"192.168.1.1", "192.168.1.2"}},
		},
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Errorf("groups-only YAML should be valid, got: %v", err)
	}
}

func TestValidateHostsDoc_GroupsAndHosts(t *testing.T) {
	// mixing hosts: and groups: is allowed
	doc := hostsFileYAML{
		Hosts:  []string{"google.com"},
		Groups: []groupYAML{{Name: "DC1", Hosts: []string{"192.168.1.1"}}},
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Errorf("mixed hosts+groups should be valid, got: %v", err)
	}
}

func TestValidateHostsDoc_EmptyGroupName(t *testing.T) {
	doc := hostsFileYAML{
		Groups: []groupYAML{{Name: "", Hosts: []string{"192.168.1.1"}}},
	}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for empty group name")
	}
}

func TestValidateHostsDoc_EmptyGroupHosts(t *testing.T) {
	doc := hostsFileYAML{
		Groups: []groupYAML{{Name: "DC1", Hosts: []string{}}},
	}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for group with no hosts")
	}
}

func TestValidateHostsDoc_EmptyGroupHostEntry(t *testing.T) {
	doc := hostsFileYAML{
		Groups: []groupYAML{{Name: "DC1", Hosts: []string{"192.168.1.1", "  "}}},
	}
	if err := validateHostsDoc(doc); err == nil {
		t.Fatal("expected error for blank host entry in group")
	}
}

func TestMergeHosts_Groups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	yaml := `hosts:
  - google.com
groups:
  - name: DC1
    hosts:
      - 192.168.1.1
      - 192.168.1.2
  - name: DC2
    hosts:
      - 10.0.1.1
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := config{hostsFile: path}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	hosts, groups, _, err := mergeHosts(cfg, fs, nil)
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}
	// ungrouped first, then DC1, then DC2
	want := []string{"google.com", "192.168.1.1", "192.168.1.2", "10.0.1.1"}
	if len(hosts) != len(want) {
		t.Fatalf("hosts: want %v, got %v", want, hosts)
	}
	for i, h := range want {
		if hosts[i] != h {
			t.Errorf("hosts[%d]: want %q, got %q", i, h, hosts[i])
		}
	}
	if len(groups) != 2 {
		t.Fatalf("groups: want 2, got %d", len(groups))
	}
	if groups[0].Name != "DC1" {
		t.Errorf("groups[0].Name: want DC1, got %q", groups[0].Name)
	}
	if len(groups[0].Indices) != 2 || groups[0].Indices[0] != 1 || groups[0].Indices[1] != 2 {
		t.Errorf("groups[0].Indices: want [1,2], got %v", groups[0].Indices)
	}
	if groups[1].Name != "DC2" {
		t.Errorf("groups[1].Name: want DC2, got %q", groups[1].Name)
	}
	if len(groups[1].Indices) != 1 || groups[1].Indices[0] != 3 {
		t.Errorf("groups[1].Indices: want [3], got %v", groups[1].Indices)
	}
}

func TestMergeHosts_GroupsOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	yaml := `groups:
  - name: DC1
    hosts:
      - 192.168.1.1
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := config{hostsFile: path}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	hosts, groups, _, err := mergeHosts(cfg, fs, nil)
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "192.168.1.1" {
		t.Fatalf("hosts: want [192.168.1.1], got %v", hosts)
	}
	if len(groups) != 1 || groups[0].Indices[0] != 0 {
		t.Fatalf("groups: want [{DC1 [0]}], got %v", groups)
	}
}

func TestParseHostsFile_GroupsYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	yaml := `groups:
  - name: "Site A"
    hosts:
      - 1.1.1.1
      - 8.8.8.8
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	doc, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if len(doc.Groups) != 1 {
		t.Fatalf("Groups: want 1, got %d", len(doc.Groups))
	}
	if doc.Groups[0].Name != "Site A" {
		t.Errorf("Groups[0].Name: want %q, got %q", "Site A", doc.Groups[0].Name)
	}
	if len(doc.Groups[0].Hosts) != 2 {
		t.Errorf("Groups[0].Hosts: want 2, got %d", len(doc.Groups[0].Hosts))
	}
}

func TestRunOnAddHost_AddsHostAndReloads(t *testing.T) {
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

	reloadCount := 0
	uiRun = func(opts ui.RunOptions) error {
		reloadCount++
		if reloadCount == 1 {
			// First run: call OnAddHost to add a new host
			if err := opts.OnAddHost("newhost.example.com"); err != nil {
				t.Errorf("OnAddHost: unexpected error: %v", err)
			}
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d; stderr: %s", code, errOut.String())
	}
	if reloadCount != 2 {
		t.Fatalf("expected 2 UI runs (initial + reload), got %d", reloadCount)
	}
}

func TestRunOnAddHost_DuplicateReturnsError(t *testing.T) {
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

	uiRun = func(opts ui.RunOptions) error {
		// Try to add the same host that already exists
		if err := opts.OnAddHost("example.com"); err == nil {
			t.Error("OnAddHost with duplicate host should return error")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestRunOnDeleteHost_RemovesHostAndReloads(t *testing.T) {
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

	reloadCount := 0
	uiRun = func(opts ui.RunOptions) error {
		reloadCount++
		if reloadCount == 1 {
			// Delete the second host (keep "host-a")
			if err := opts.OnDeleteHost("host-b"); err != nil {
				t.Errorf("OnDeleteHost: unexpected error: %v", err)
			}
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "host-a", "host-b"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d; stderr: %s", code, errOut.String())
	}
	if reloadCount != 2 {
		t.Fatalf("expected 2 UI runs (initial + reload), got %d", reloadCount)
	}
}

func TestRunOnDeleteHost_LastHostReturnsError(t *testing.T) {
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

	uiRun = func(opts ui.RunOptions) error {
		// Try to delete the only host
		if err := opts.OnDeleteHost("example.com"); err == nil {
			t.Error("OnDeleteHost of last host should return error")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestRunOnDeleteHost_NotFoundReturnsError(t *testing.T) {
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

	uiRun = func(opts ui.RunOptions) error {
		if err := opts.OnDeleteHost("nonexistent.host"); err == nil {
			t.Error("OnDeleteHost of nonexistent host should return error")
		}
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

// ---- setupLogger additional coverage ----

func TestSetupLogger_EmptyPath(t *testing.T) {
	f, err := setupLogger("")
	if err != nil {
		t.Fatalf("setupLogger(\"\") unexpected error: %v", err)
	}
	if f != nil {
		t.Error("expected nil file handle for empty path")
	}
}

func TestSetupLogger_InvalidPath(t *testing.T) {
	_, err := setupLogger("/nonexistent-dir-mping-test/out.csv")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

// ---- writeJSONSnapshot error path ----

func TestWriteJSONSnapshot_WriteError(t *testing.T) {
	err := writeJSONSnapshot("/nonexistent-dir-mping-test/stats.json", nil, nil)
	if err == nil {
		t.Error("expected error when parent directory does not exist")
	}
}

// ---- setupHTTPChecker ----

func TestSetupHTTPChecker_NoURLs(t *testing.T) {
	hc := setupHTTPChecker(nil, time.Second, time.Second)
	if hc != nil {
		t.Error("expected nil HTTPChecker for empty URL list")
	}
}

func TestSetupHTTPChecker_WithURLs(t *testing.T) {
	srv := &localHTTPServer{}
	srv.start(t)
	hc := setupHTTPChecker([]string{srv.url()}, 100*time.Millisecond, 100*time.Millisecond)
	if hc == nil {
		t.Fatal("expected non-nil HTTPChecker for non-empty URL list")
	}
	hc.Stop()
}

// localHTTPServer is a minimal test HTTP server.
type localHTTPServer struct {
	l net.Listener
}

func (s *localHTTPServer) start(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.l = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Respond with minimal HTTP 200.
			_, _ = fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
			c.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
}

func (s *localHTTPServer) url() string {
	return "http://" + s.l.Addr().String()
}

// ---- printExitSummary additional coverage ----

func TestPrintExitSummary_WithRecv(t *testing.T) {
	tgt := stats.NewTargetStats("example.com")
	tgt.SetIP("1.2.3.4")
	tgt.IncSent()
	tgt.OnSuccess(10*time.Millisecond, 64)

	var out bytes.Buffer
	printExitSummary(&out, []*stats.TargetStats{tgt})
	s := out.String()
	if !strings.Contains(s, "example.com") {
		t.Errorf("missing host in summary: %q", s)
	}
	if !strings.Contains(s, "rtt min") {
		t.Errorf("missing rtt line in summary: %q", s)
	}
}

func TestNewCustomResolver(t *testing.T) {
	r := newCustomResolver("8.8.8.8")
	if r == nil {
		t.Fatal("expected resolver, got nil")
	}

	rPort := newCustomResolver("8.8.8.8:53")
	if rPort == nil {
		t.Fatal("expected resolver with port, got nil")
	}
}

func TestExpandTargets(t *testing.T) {
	// Without resolveAll
	hosts := []string{"example.com", "8.8.8.8"}
	groups := []ui.TargetGroup{
		{Name: "G1", Indices: []int{0}},
	}
	expandedHosts, _, err := expandTargets(hosts, groups, config{resolveAll: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expandedHosts) != 2 || expandedHosts[0] != "example.com" {
		t.Errorf("expected original hosts, got %v", expandedHosts)
	}

	// With resolveAll but already expanded/pinned
	hostsPinned := []string{"example.com (1.1.1.1)", "8.8.8.8"}
	expandedHostsPinned, _, err := expandTargets(hostsPinned, groups, config{resolveAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expandedHostsPinned) != 2 || expandedHostsPinned[0] != "example.com (1.1.1.1)" {
		t.Errorf("expected pinned hosts, got %v", expandedHostsPinned)
	}
}
