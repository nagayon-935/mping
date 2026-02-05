package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

type fakePinger struct {
	startErr     error
	started      bool
	closed       bool
	waited       bool
	discoverMTU  int
	discoverErr  error
	traceErr     error
	logWriterSet bool
}

func (f *fakePinger) Start(privileged bool, interval, timeout time.Duration) error {
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

func (f *fakePinger) DiscoverMaxPayload(dest string, start int, min int, privileged bool, logf func(string)) (int, error) {
	if f.discoverErr != nil {
		return 0, f.discoverErr
	}
	if f.discoverMTU == 0 {
		return start, nil
	}
	return f.discoverMTU, nil
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
	cfg, hosts, _, err := parseArgs([]string{"example.com"})
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
	_, _, _, err := parseArgs([]string{})
	if err == nil {
		t.Fatal("expected error for missing hosts")
	}
}

func TestParseArgsHostsFileAllowsNoHosts(t *testing.T) {
	cfg, hosts, _, err := parseArgs([]string{"--file", "hosts.yaml"})
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
	_, _, _, err := parseArgs([]string{"-4", "-6", "example.com"})
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

func TestParseHostsFileYAMLSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("- a\n- b\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("hosts: got %v", got)
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
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("hosts: got %v", got)
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
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, onStop func(), onRestart func() error) error {
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
	uiRun = func(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, onStop func(), onRestart func() error) error {
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
	if err := os.WriteFile(path, []byte("- a\n- b\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := config{hostsFile: path}
	got, err := mergeHosts(cfg, []string{"c"})
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
