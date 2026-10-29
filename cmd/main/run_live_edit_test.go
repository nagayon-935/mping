package main

import (
	"bytes"
	"testing"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

// TestRun_LiveHostEdits drives the TUI's OnAddHost / OnDeleteHost callbacks
// through run(): valid edits apply live to the running session (one
// persistent UI run, no reload), and rejected edits leave the target list
// untouched.
func TestRun_LiveHostEdits(t *testing.T) {
	tests := []struct {
		name        string
		hosts       []string
		edit        func(ui.RunOptions) error
		wantErr     bool
		wantTargets int
	}{
		{"adding a new host keeps the session", []string{"example.com"}, func(o ui.RunOptions) error { return o.OnAddHost("newhost.example.com") }, false, 2},
		{"adding a duplicate host is rejected", []string{"example.com"}, func(o ui.RunOptions) error { return o.OnAddHost("example.com") }, true, 1},
		{"deleting one host keeps the session", []string{"host-a", "host-b"}, func(o ui.RunOptions) error { return o.OnDeleteHost("host-b") }, false, 1},
		{"deleting the last host is rejected", []string{"example.com"}, func(o ui.RunOptions) error { return o.OnDeleteHost("example.com") }, true, 1},
		{"deleting an unknown host is rejected", []string{"example.com"}, func(o ui.RunOptions) error { return o.OnDeleteHost("nonexistent.host") }, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uiRuns := 0
			stubRunSeams(t, func(opts ui.RunOptions) error {
				uiRuns++
				if err := tt.edit(opts); (err != nil) != tt.wantErr {
					t.Errorf("edit error = %v, wantErr %v", err, tt.wantErr)
				}
				if got := len(opts.TargetSource().Targets); got != tt.wantTargets {
					t.Errorf("live target count = %d, want %d", got, tt.wantTargets)
				}
				return nil
			})
			newPinger = func([]*stats.TargetStats, pinger.Options) pingerController { return newLiveFakePinger() }

			var out, errOut bytes.Buffer
			code := run(append([]string{"-S", "10.0.0.2"}, tt.hosts...), &out, &errOut)

			if code != 0 {
				t.Fatalf("expected 0, got %d; stderr: %s", code, errOut.String())
			}
			if uiRuns != 1 {
				t.Fatalf("expected one persistent UI run, got %d", uiRuns)
			}
		})
	}
}
