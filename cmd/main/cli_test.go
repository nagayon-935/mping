package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseArgs_Defaults(t *testing.T) {
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
	if cfg.duration != 0 {
		t.Errorf("expected duration disabled (0) by default, got %s", cfg.duration)
	}
	if cfg.webEnabled || cfg.webPort != 8080 {
		t.Errorf("web = (%v, %d), want (false, 8080)", cfg.webEnabled, cfg.webPort)
	}
	th := cfg.thresholds
	if th.RTTWarn != 50*time.Millisecond || th.RTTCrit != 200*time.Millisecond {
		t.Errorf("rtt defaults: got warn=%v crit=%v, want 50ms/200ms", th.RTTWarn, th.RTTCrit)
	}
	if th.JitterWarn != 10*time.Millisecond || th.JitterCrit != 50*time.Millisecond {
		t.Errorf("jitter defaults: got warn=%v crit=%v, want 10ms/50ms", th.JitterWarn, th.JitterCrit)
	}
	if th.LossWarn != 20 || th.LossCrit != 80 {
		t.Errorf("loss defaults: got warn=%g crit=%g, want 20/80", th.LossWarn, th.LossCrit)
	}
	if err := th.Validate(); err != nil {
		t.Errorf("default thresholds should validate: %v", err)
	}
}

func TestParseArgs_Errors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing hosts", []string{}},
		{"-4 and -6 together", []string{"-4", "-6", "example.com"}},
		{"negative --duration", []string{"--duration", "-5s", "example.com"}},
		{"malformed --duration", []string{"--duration", "notaduration", "example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, _, err := parseArgs(tt.args); err == nil {
				t.Fatalf("expected an error for %v", tt.args)
			}
		})
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

func TestParseArgsNoTUIDefaultsOff(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"example.com"})
	if err != nil {
		t.Fatal(err)
	}
	on, _, _, _, err := parseArgs([]string{"--no-tui", "example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.noTUI || !on.noTUI {
		t.Fatalf("noTUI default=%v with flag=%v, want false/true", cfg.noTUI, on.noTUI)
	}
}

func TestParseArgsDuration_Valid(t *testing.T) {
	tests := []struct {
		arg  string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"5m", 5 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"0s", 0},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			cfg, _, _, _, err := parseArgs([]string{"--duration", tt.arg, "example.com"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.duration != tt.want {
				t.Fatalf("duration: got %s, want %s", cfg.duration, tt.want)
			}
		})
	}
}

func TestParseArgs_JSONOutputFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"short -j", []string{"-j", "/tmp/stats.json", "example.com"}, "/tmp/stats.json"},
		{"long --json-output", []string{"--json-output", "/tmp/out.json", "example.com"}, "/tmp/out.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, _, _, err := parseArgs(tt.args)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if cfg.jsonOutputFile != tt.want {
				t.Errorf("jsonOutputFile = %q, want %q", cfg.jsonOutputFile, tt.want)
			}
		})
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
	th := cfg.thresholds
	if th.RTTWarn != 30*time.Millisecond || th.RTTCrit != 100*time.Millisecond {
		t.Errorf("rtt: got %v/%v, want 30ms/100ms", th.RTTWarn, th.RTTCrit)
	}
	if th.JitterWarn != 5*time.Millisecond || th.JitterCrit != 20*time.Millisecond {
		t.Errorf("jitter: got %v/%v, want 5ms/20ms", th.JitterWarn, th.JitterCrit)
	}
	if th.LossWarn != 10 || th.LossCrit != 50 {
		t.Errorf("loss: got %g/%g, want 10/50", th.LossWarn, th.LossCrit)
	}
	if err := th.Validate(); err != nil {
		t.Errorf("custom thresholds should validate: %v", err)
	}
}
