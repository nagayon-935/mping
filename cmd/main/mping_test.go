package main

import "testing"

func TestGetPreferredOutboundIP_Localhost(t *testing.T) {
	ip := getPreferredOutboundIP("127.0.0.1", "udp")
	if ip == "" {
		t.Fatal("expected a non-empty local IP")
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
