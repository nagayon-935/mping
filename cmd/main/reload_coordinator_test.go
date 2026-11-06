package main

import (
	"errors"
	"regexp"
	"testing"

	"github.com/nagayon-935/mping/internal/ui"
)

func TestRequestFileReloadErrorFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, wantMessage string
		wantLiveCall            bool
	}{
		{"parse", "hosts: [\n", `Reload error: .+`, false},
		{"validation", "hosts: []\n", `Reload validation error: .+`, false},
		{"live apply", "hosts: [192.0.2.1]\n", `Reload error: live  apply failed`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeHostsYAML(t, tc.yaml)
			cfg, hosts, fs, _, err := parseArgs([]string{"-f", path})
			if err != nil {
				t.Fatal(err)
			}
			rc := newReloadCoordinator(fs, cfg, hosts)
			liveCalled := false
			rc.tryLive = func(config, []targetSpec, []ui.TargetGroup) (bool, string, error) {
				liveCalled = true
				return false, "", errors.New("live  apply failed")
			}
			sig := newReloadSignal()
			logs := make(chan string, 2)
			rc.requestFileReload(sig, path, logs)

			select {
			case got := <-logs:
				// Keep the timestamp, separator and colour reset identical for
				// every reload error, preserving whitespace inside the error.
				pattern := `^\[red\]\[[0-9]{2}:[0-9]{2}:[0-9]{2}\] ` + tc.wantMessage + `\[-\]$`
				if !regexp.MustCompile(pattern).MatchString(got) {
					t.Errorf("reload log = %q, want pattern %q", got, pattern)
				}
			default:
				t.Fatal("reload error was not logged")
			}
			if len(logs) != 0 {
				t.Fatal("reload error logged more than once")
			}
			if liveCalled != tc.wantLiveCall {
				t.Errorf("live reload called = %v, want %v", liveCalled, tc.wantLiveCall)
			}
			if closed(sig.ch) || rc.pending != nil {
				t.Fatal("failed reload queued a restart")
			}
		})
	}
}
