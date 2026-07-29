package main

import (
	"errors"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// newStateTestSupervisor builds a supervisor whose pinger factory records
// every pinger it hands out, so a test can assert lifecycle effects without
// opening a real raw socket.
func newStateTestSupervisor(t *testing.T) (*supervisor, *[]*lifecycleFakePinger) {
	t.Helper()
	var created []*lifecycleFakePinger
	sup := newSupervisor(supervisorConfig{
		makePinger: func(size int) pingerController {
			fp := &lifecycleFakePinger{}
			created = append(created, fp)
			return fp
		},
		targets:  []*stats.TargetStats{stats.NewTargetStats("example.com")},
		interval: time.Second,
		timeout:  time.Second,
		logCh:    make(chan string, 8),
	})
	return sup, &created
}

func doCmd(t *testing.T, s *supervisor, k cmdKind) error {
	t.Helper()
	return s.handle(command{kind: k})
}

// TestHandle_CommandTable walks every (state, command) cell of the design's
// command table. The old code expressed these rules four different ways
// across resetTrace/resetMTR/resetPort/resetHTTP, which is how D1 (resets
// resurrecting components after shutdown) survived. One table, one rule set.
func TestHandle_CommandTable(t *testing.T) {
	tests := []struct {
		name      string
		from      supervisorState
		cmd       cmdKind
		wantState supervisorState
		wantErr   error
	}{
		{"start from stopped", stateStopped, cmdStart, stateRunning, nil},
		{"stop from stopped", stateStopped, cmdStop, stateStopped, nil},
		{"restart from stopped", stateStopped, cmdRestart, stateRunning, nil},
		{"resetTrace from stopped", stateStopped, cmdResetTrace, stateStopped, nil},
		{"resetMTR from stopped", stateStopped, cmdResetMTR, stateStopped, nil},
		{"resetPort from stopped", stateStopped, cmdResetPort, stateStopped, nil},
		{"resetHTTP from stopped", stateStopped, cmdResetHTTP, stateStopped, nil},
		{"terminate from stopped", stateStopped, cmdTerminate, stateTerminated, nil},

		{"start from running", stateRunning, cmdStart, stateRunning, nil},
		{"stop from running", stateRunning, cmdStop, stateStopped, nil},
		{"restart from running", stateRunning, cmdRestart, stateRunning, nil},
		{"resetTrace from running", stateRunning, cmdResetTrace, stateRunning, nil},
		{"resetMTR from running", stateRunning, cmdResetMTR, stateRunning, nil},
		{"resetPort from running", stateRunning, cmdResetPort, stateRunning, nil},
		{"resetHTTP from running", stateRunning, cmdResetHTTP, stateRunning, nil},
		{"terminate from running", stateRunning, cmdTerminate, stateTerminated, nil},

		// Nothing may revive a terminated supervisor.
		{"start from terminated", stateTerminated, cmdStart, stateTerminated, errSupervisorTerminated},
		{"stop from terminated", stateTerminated, cmdStop, stateTerminated, nil},
		{"restart from terminated", stateTerminated, cmdRestart, stateTerminated, errSupervisorTerminated},
		{"resetTrace from terminated", stateTerminated, cmdResetTrace, stateTerminated, nil},
		{"resetMTR from terminated", stateTerminated, cmdResetMTR, stateTerminated, nil},
		{"resetPort from terminated", stateTerminated, cmdResetPort, stateTerminated, nil},
		{"resetHTTP from terminated", stateTerminated, cmdResetHTTP, stateTerminated, nil},
		{"terminate from terminated", stateTerminated, cmdTerminate, stateTerminated, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sup, _ := newStateTestSupervisor(t)
			// Reach the starting state through real commands rather than
			// poking the field, so the fixture can't construct a state the
			// machine would never produce.
			switch tc.from {
			case stateRunning:
				if err := doCmd(t, sup, cmdStart); err != nil {
					t.Fatalf("setup cmdStart: %v", err)
				}
			case stateTerminated:
				if err := doCmd(t, sup, cmdTerminate); err != nil {
					t.Fatalf("setup cmdTerminate: %v", err)
				}
			}

			err := doCmd(t, sup, tc.cmd)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if sup.state != tc.wantState {
				t.Errorf("state = %v, want %v", sup.state, tc.wantState)
			}
		})
	}
}

// TestHandle_ResetAfterTerminateCreatesNothing is the D1 regression guard.
// Reproduction was: press 'R' then 's'. The reset goroutines landed after
// stopAll and installed a fresh MTR engine and traceroute goroutine probing
// an already-Closed pinger.
func TestHandle_ResetAfterTerminateCreatesNothing(t *testing.T) {
	sup, _ := newStateTestSupervisor(t)
	sup.cfg.mtrEnabled = true
	sup.cfg.traceEnabled = true

	if err := doCmd(t, sup, cmdStart); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	if err := doCmd(t, sup, cmdTerminate); err != nil {
		t.Fatalf("cmdTerminate: %v", err)
	}

	engineAfter := sup.mtrEngine
	traceDoneAfter := sup.traceDone

	for _, k := range []cmdKind{cmdResetTrace, cmdResetMTR, cmdResetPort, cmdResetHTTP} {
		if err := doCmd(t, sup, k); err != nil {
			t.Fatalf("reset %v after terminate: %v", k, err)
		}
	}

	if sup.mtrEngine != engineAfter {
		t.Error("resetMTR after terminate installed a new MTR engine")
	}
	if sup.traceDone != traceDoneAfter {
		t.Error("resetTrace after terminate started a new traceroute goroutine")
	}
	if sup.state != stateTerminated {
		t.Errorf("state = %v, want stateTerminated", sup.state)
	}
}

// TestHandle_RestartReleasesPreviousPinger replaces the concurrency-window
// coverage that TestStartPingerReleasesSupersededPinger provided: with
// commands serialized, a superseded pinger can only appear through
// sequential restarts, and each one must still be fully released.
func TestHandle_RestartReleasesPreviousPinger(t *testing.T) {
	sup, created := newStateTestSupervisor(t)

	if err := doCmd(t, sup, cmdStart); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	if err := doCmd(t, sup, cmdRestart); err != nil {
		t.Fatalf("cmdRestart: %v", err)
	}

	all := *created
	if len(all) != 2 {
		t.Fatalf("expected 2 pingers created, got %d", len(all))
	}
	if !all[0].isReleased() {
		t.Errorf("superseded pinger not released (stopped=%v closed=%v)", all[0].stopped, all[0].closed)
	}
	if pingerController(all[1]) != sup.p {
		t.Error("expected the second pinger to be the live one")
	}
}
