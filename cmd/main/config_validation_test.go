package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupAndReloadValidateEffectiveConfig(t *testing.T) {
	cases := []struct {
		name, yaml string
		args       []string
		valid      bool
	}{
		{name: "zero interval", yaml: "interval: 0"},
		{name: "negative interval", yaml: "interval: -1"},
		{name: "large interval", yaml: "interval: 60001"},
		{name: "zero timeout", yaml: "timeout: 0"},
		{name: "negative size", yaml: "size: -1"},
		{name: "large size", yaml: "size: 9873"},
		{name: "negative count", yaml: "count: -1"},
		{name: "invalid global DSCP", yaml: "dscp: bad"},
		{name: "invalid host DSCP", yaml: "groups: [{name: qos, hosts: [{host: '::1', dscp: bad}]}]"},
		{name: "invalid group", yaml: "groups: [{name: empty, hosts: []}]"},
		{name: "invalid thresholds", yaml: "thresholds: {rtt-warn: 300}"},
		{name: "CLI interval wins", yaml: "interval: 0", args: []string{"-i", "200"}, valid: true},
		{name: "CLI DSCP wins", yaml: "dscp: bad", args: []string{"--dscp", "EF"}, valid: true},
		{name: "CLI thresholds win", yaml: "thresholds: {rtt-warn: 300}", args: []string{"--rtt-crit", "500"}, valid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hosts.yaml")
			if err := os.WriteFile(path, []byte("hosts: [127.0.0.1]\n"+tc.yaml+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-4", "-f", path}, tc.args...)
			var out, errs bytes.Buffer
			_, _, ok := parseAndLoadHosts(args, &out, &errs)
			if ok != tc.valid {
				t.Fatalf("startup accepted=%v want=%v: %s", ok, tc.valid, errs.String())
			}
			cfg, hosts, fs, _, err := parseArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			rc := newReloadCoordinator(fs, cfg, hosts)
			sig := newReloadSignal()
			logs := make(chan string, 1)
			rc.requestFileReload(sig, path, logs)
			select {
			case <-sig.ch:
				if !tc.valid {
					t.Fatal("invalid reload stopped the running iteration")
				}
			default:
				if tc.valid {
					t.Fatal("valid reload was rejected")
				}
			}
			if !tc.valid {
				select {
				case msg := <-logs:
					if !strings.Contains(msg, "validation") {
						t.Fatalf("missing validation error: %s", msg)
					}
				default:
					t.Fatal("rejected reload did not report a validation error")
				}
			}
		})
	}
}

func TestConfigOnlyFileAllowsCLIHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("interval: 200\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	sp, _, ok := parseAndLoadHosts([]string{"-4", "-f", path, "127.0.0.1"}, &out, &errs)
	if !ok || len(sp.hosts) != 1 || sp.cfg.intervalMs != 200 {
		t.Fatalf("config-only file failed: %s", errs.String())
	}
	rc := newReloadCoordinator(sp.fs, sp.cliCfg, sp.cliHosts)
	sig := newReloadSignal()
	rc.requestFileReload(sig, path, make(chan string, 1))
	select {
	case <-sig.ch:
	default:
		t.Fatal("config-only file cannot reload with CLI hosts")
	}
}
