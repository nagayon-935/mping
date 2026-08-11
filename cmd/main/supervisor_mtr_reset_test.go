package main

import (
	"testing"
	"time"
)

// recordOnePing gives the fixture target one sent-and-answered probe so a
// later assertion can tell "stats survived" from "stats were wiped".
func recordOnePing(t *testing.T, s *supervisor) {
	t.Helper()
	tgt := s.cfg.targets[0]
	tgt.IncSent()
	tgt.OnSuccess(5*time.Millisecond, 64)
}

// TestResetMTR_PreservesPingStats is the decoupling guard. restartMTR used
// to call Reset() on every target, which clears the ping counters (and, via
// TargetStats.Reset, the MTR hop stats) as a side effect of replacing the
// MTR engine. Clearing ping stats is the 'R' key's job — input_handler.go
// already resets every target there — so the MTR engine swap must not do it
// a second time from inside the supervisor.
func TestResetMTR_PreservesPingStats(t *testing.T) {
	sup, _ := newStateTestSupervisor(t)
	sup.cfg.mtrEnabled = true
	if err := doCmd(t, sup, cmdStart); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	defer func() { _ = doCmd(t, sup, cmdTerminate) }()
	recordOnePing(t, sup)

	if err := doCmd(t, sup, cmdResetMTR); err != nil {
		t.Fatalf("cmdResetMTR: %v", err)
	}

	v := sup.cfg.targets[0].GetView()
	if v.Sent != 1 || v.Recv != 1 {
		t.Errorf("after cmdResetMTR: Sent=%d Recv=%d, want 1/1 — the MTR engine swap wiped the ping counters", v.Sent, v.Recv)
	}
}

// TestRestart_PreservesPingStatsRegardlessOfMTR pins the consistency this
// decoupling buys. restartMTR is also reached from startAll, so a restart
// used to wipe the ping counters when --mtr was on and keep them when it was
// off — the same command with two different outcomes depending on an
// unrelated flag. Both cases must now preserve them.
func TestRestart_PreservesPingStatsRegardlessOfMTR(t *testing.T) {
	for _, mtrEnabled := range []bool{false, true} {
		name := "mtr disabled"
		if mtrEnabled {
			name = "mtr enabled"
		}
		t.Run(name, func(t *testing.T) {
			sup, _ := newStateTestSupervisor(t)
			sup.cfg.mtrEnabled = mtrEnabled
			if err := doCmd(t, sup, cmdStart); err != nil {
				t.Fatalf("cmdStart: %v", err)
			}
			defer func() { _ = doCmd(t, sup, cmdTerminate) }()
			recordOnePing(t, sup)

			if err := doCmd(t, sup, cmdRestart); err != nil {
				t.Fatalf("cmdRestart: %v", err)
			}

			v := sup.cfg.targets[0].GetView()
			if v.Sent != 1 || v.Recv != 1 {
				t.Errorf("after cmdRestart: Sent=%d Recv=%d, want 1/1", v.Sent, v.Recv)
			}
		})
	}
}
