package main

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/hostsfile"
	"github.com/spf13/pflag"
)

// TestApplyDocToCfg_Duration covers the YAML `duration:` key: applied when no
// flag was given, ignored when --duration was passed explicitly, and rejected
// when malformed.
func TestApplyDocToCfg_Duration(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		yaml     string
		want     time.Duration
		wantFail bool
	}{
		{"YAML applied when no flag", []string{"example.com"}, "5m", 5 * time.Minute, false},
		{"explicit flag overrides YAML", []string{"--duration", "1m", "example.com"}, "5m", time.Minute, false},
		{"malformed YAML is rejected", []string{"example.com"}, "not-a-duration", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, fs, _, err := parseArgs(tt.args)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			v := tt.yaml
			_, _, gotCfg, err := applyDocToCfg(cfg, fs, hostsfile.Document{Hosts: hostEntries("example.com"), Duration: &v})
			if tt.wantFail {
				if err == nil {
					t.Fatal("expected error for malformed duration in YAML doc")
				}
				return
			}
			if err != nil {
				t.Fatalf("applyDocToCfg: %v", err)
			}
			if gotCfg.duration != tt.want {
				t.Errorf("duration: got %s, want %s (a YAML value must not override an explicit flag)", gotCfg.duration, tt.want)
			}
		})
	}
}

func TestOverlayThresholdsDoc_YAMLApplied(t *testing.T) {
	// No threshold flags set → YAML values apply.
	cfg, _, fs, _, err := parseArgs([]string{"example.com"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	mi := func(v int) *int { return &v }
	mf := func(v float64) *float64 { return &v }
	cfg.thresholds = overlayThresholdsDoc(cfg.thresholds, fs, &hostsfile.Thresholds{
		RTTWarn: mi(25), RTTCrit: mi(90),
		LossWarn: mf(15), LossCrit: mf(60),
	})
	if cfg.thresholds.RTTWarn != 25*time.Millisecond || cfg.thresholds.RTTCrit != 90*time.Millisecond {
		t.Errorf("rtt from YAML: got %v/%v, want 25ms/90ms", cfg.thresholds.RTTWarn, cfg.thresholds.RTTCrit)
	}
	if cfg.thresholds.LossWarn != 15 || cfg.thresholds.LossCrit != 60 {
		t.Errorf("loss from YAML: got %g/%g, want 15/60", cfg.thresholds.LossWarn, cfg.thresholds.LossCrit)
	}
	// Untouched fields keep flag defaults.
	if cfg.thresholds.JitterWarn != 10*time.Millisecond || cfg.thresholds.JitterCrit != 50*time.Millisecond {
		t.Errorf("jitter should keep defaults: got %v/%v", cfg.thresholds.JitterWarn, cfg.thresholds.JitterCrit)
	}
}

func TestOverlayThresholdsDoc_FlagOverridesYAML(t *testing.T) {
	// rtt-warn set on CLI → YAML rtt-warn ignored, others apply.
	cfg, _, fs, _, err := parseArgs([]string{"--rtt-warn", "40", "example.com"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	mi := func(v int) *int { return &v }
	cfg.thresholds = overlayThresholdsDoc(cfg.thresholds, fs, &hostsfile.Thresholds{
		RTTWarn: mi(25), RTTCrit: mi(90),
	})
	if cfg.thresholds.RTTWarn != 40*time.Millisecond {
		t.Errorf("CLI rtt-warn should win: got %v, want 40ms", cfg.thresholds.RTTWarn)
	}
	if cfg.thresholds.RTTCrit != 90*time.Millisecond {
		t.Errorf("YAML rtt-crit should apply: got %v, want 90ms", cfg.thresholds.RTTCrit)
	}
}

// TestMergeHosts checks that hosts from the file come first and hosts given on
// the command line are appended after them.
func TestMergeHosts(t *testing.T) {
	path := writeHostsYAML(t, "hosts:\n  - a\n  - b\n")
	cfg, _, _, _, err := parseArgs([]string{"-f", path})
	if err != nil {
		t.Fatal(err)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	got, _, _, err := mergeHosts(cfg, fs, []string{"c"})
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("hosts len: got %d", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Host != want {
			t.Errorf("hosts[%d] = %q, want %q", i, got[i].Host, want)
		}
	}
}

// TestMergeHosts_PerTargetDSCP exercises the feature's primary use case end
// to end through the real hosts-file pipeline: the same destination
// monitored twice under two different DSCP markings, plus a global default
// and a plain (no-override) host.
func TestMergeHosts_PerTargetDSCP(t *testing.T) {
	yamlContent := `
dscp: CS0
hosts:
  - host: 2001:db8::1
    dscp: EF
  - host: 2001:db8::1
    dscp: DF
  - plain.example.com
`
	path := writeHostsYAML(t, yamlContent)
	cfg, _, _, _, err := parseArgs([]string{"-f", path})
	if err != nil {
		t.Fatal(err)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)

	got, _, mergedCfg, err := mergeHosts(cfg, fs, nil)
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}
	if mergedCfg.dscp != "CS0" {
		t.Fatalf("mergedCfg.dscp = %q, want %q", mergedCfg.dscp, "CS0")
	}
	if len(got) != 3 {
		t.Fatalf("hosts len: got %d, want 3", len(got))
	}
	if got[0].Host != "2001:db8::1" || got[0].DSCP != "EF" {
		t.Fatalf("got[0] = %+v, want Host=2001:db8::1 DSCP=EF", got[0])
	}
	if got[1].Host != "2001:db8::1" || got[1].DSCP != "DF" {
		t.Fatalf("got[1] = %+v, want Host=2001:db8::1 DSCP=DF", got[1])
	}
	if got[2].Host != "plain.example.com" || got[2].DSCP != "" {
		t.Fatalf("got[2] = %+v, want Host=plain.example.com DSCP=\"\"", got[2])
	}
}

func TestMergeHosts_Options(t *testing.T) {
	// Interface existence is now checked before a configuration is admitted;
	// this merge fixture must not depend on the developer machine having eth0.
	originalInterface := interfaceByName
	interfaceByName = func(name string) (*net.Interface, error) {
		if name == "eth0" {
			return &net.Interface{Name: name}, nil
		}
		return originalInterface(name)
	}
	t.Cleanup(func() { interfaceByName = originalInterface })

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
ptr: true
ipv4: true
port:
  - 80/tcp
`
	path := writeHostsYAML(t, yamlContent)

	cfg, _, _, _, err := parseArgs([]string{"-f", path})
	if err != nil {
		t.Fatal(err)
	}
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
	fs.Bool("ptr", false, "")
	fs.Bool("ipv4", false, "")
	fs.StringSlice("port", nil, "")

	gotHosts, _, gotCfg, err := mergeHosts(cfg, fs, nil)
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}

	if len(gotHosts) != 1 || gotHosts[0].Host != "example.com" {
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
	if gotCfg.ptrEnabled != true {
		t.Errorf("ptrEnabled: got %v", gotCfg.ptrEnabled)
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
	path := writeHostsYAML(t, "hosts:\n  - a\nipv4: true\nipv6: true\n")
	cfg, _, _, _, err := parseArgs([]string{"-f", path})
	if err != nil {
		t.Fatal(err)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	_, _, _, err = mergeHosts(cfg, fs, nil)
	if err == nil {
		t.Fatal("expected error for ipv4+ipv6 conflict, got nil")
	}
	if !strings.Contains(err.Error(), "cannot use both -4 and -6") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestMergeHosts_Groups(t *testing.T) {
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
	path := writeHostsYAML(t, yaml)
	cfg, _, _, _, err := parseArgs([]string{"-f", path})
	if err != nil {
		t.Fatal(err)
	}
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
		if hosts[i].Host != h {
			t.Errorf("hosts[%d]: want %q, got %q", i, h, hosts[i].Host)
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
	yaml := `groups:
  - name: DC1
    hosts:
      - 192.168.1.1
`
	path := writeHostsYAML(t, yaml)
	cfg, _, _, _, err := parseArgs([]string{"-f", path})
	if err != nil {
		t.Fatal(err)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	hosts, groups, _, err := mergeHosts(cfg, fs, nil)
	if err != nil {
		t.Fatalf("mergeHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Host != "192.168.1.1" {
		t.Fatalf("hosts: want [192.168.1.1], got %v", hosts)
	}
	if len(groups) != 1 || groups[0].Indices[0] != 0 {
		t.Fatalf("groups: want [{DC1 [0]}], got %v", groups)
	}
}
