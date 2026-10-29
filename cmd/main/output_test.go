package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

func TestSetupLogger_PathHandling(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantNil bool
		wantErr bool
	}{
		{"empty path disables logging", "", true, false},
		{"unwritable directory is an error", "/nonexistent-dir-mping-test/out.csv", true, true},
		{"writable path opens a file", filepath.Join(t.TempDir(), "out.csv"), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := setupLogger(tt.path)
			if f != nil {
				defer f.Close()
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("setupLogger(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
			if (f == nil) != tt.wantNil {
				t.Fatalf("setupLogger(%q) file = %v, wantNil %v", tt.path, f, tt.wantNil)
			}
		})
	}
}

// TestSetupLogger_WritesHeaderOncePerFile reopens the same CSV: the first
// open creates the file with its header, and a second open of the now
// non-empty file must not append the header again.
func TestSetupLogger_WritesHeaderOncePerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")

	for i := range 2 {
		f, err := setupLogger(path)
		if err != nil {
			t.Fatalf("setupLogger call %d: %v", i+1, err)
		}
		f.Close()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	const header = "Timestamp,Host,IP,Seq,Status,RTT(ms),TTL,Error\n"
	if count := strings.Count(string(data), header); count != 1 {
		t.Errorf("header appears %d times, want exactly 1", count)
	}
}

func TestWriteJSONSnapshot(t *testing.T) {
	withTarget := stats.NewTargetStats("example.com")
	withTarget.SetIP("1.2.3.4")
	tests := []struct {
		name    string
		targets []*stats.TargetStats
	}{
		{"with a target", []*stats.TargetStats{withTarget}},
		{"without targets", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stats.json")

			if err := writeJSONSnapshot(path, tt.targets, nil); err != nil {
				t.Fatalf("writeJSONSnapshot: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("expected final file to exist: %v", err)
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			for _, key := range []string{"timestamp", "targets"} {
				if _, ok := out[key]; !ok {
					t.Errorf("missing %q in JSON", key)
				}
			}
			// The snapshot is written to a temp file and renamed into place.
			if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
				t.Error("expected .tmp file to be removed after rename")
			}
		})
	}
}

func TestWriteJSONSnapshot_WriteError(t *testing.T) {
	err := writeJSONSnapshot("/nonexistent-dir-mping-test/stats.json", nil, nil)
	if err == nil {
		t.Error("expected error when parent directory does not exist")
	}
}

func TestAllTargetsUnresponsive(t *testing.T) {
	allLoss := []*stats.TargetStats{stats.NewTargetStats("a"), stats.NewTargetStats("b")}
	for _, tgt := range allLoss {
		tgt.IncSent()
	}
	if !allTargetsUnresponsive(allLoss) {
		t.Error("expected true when no target has received a reply")
	}

	partial := []*stats.TargetStats{stats.NewTargetStats("a"), stats.NewTargetStats("b")}
	partial[0].IncSent()
	partial[0].OnSuccess(5*time.Millisecond, 64)
	partial[1].IncSent()
	if allTargetsUnresponsive(partial) {
		t.Error("expected false when at least one target received a reply")
	}

	if !allTargetsUnresponsive(nil) {
		t.Error("expected true for an empty target list")
	}
}

// TestPrintExitSummary pins the per-target summary lines. The duplicates and
// late-reply segments mirror ping.c's summary and must be omitted entirely
// when there are none (no stray ", +0 duplicates").
func TestPrintExitSummary(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*stats.TargetStats)
		want    []string
		wantNot []string
	}{
		{
			name: "target with replies shows host and rtt line",
			setup: func(tgt *stats.TargetStats) {
				tgt.IncSent()
				tgt.OnSuccess(10*time.Millisecond, 64)
			},
			want:    []string{"example.com", "rtt min"},
			wantNot: []string{"duplicates", "out of wait time"},
		},
		{
			// Classic dataset: population stddev of {2,4,4,4,5,5,7,9}ms is exactly 2ms.
			name: "stddev is printed",
			setup: func(tgt *stats.TargetStats) {
				for _, ms := range []int{2, 4, 4, 4, 5, 5, 7, 9} {
					tgt.IncSent()
					tgt.OnSuccess(time.Duration(ms)*time.Millisecond, 64)
				}
			},
			want: []string{"rtt min/avg/max/stddev = ", "/2.000 ms"},
		},
		{
			name: "duplicates segment",
			setup: func(tgt *stats.TargetStats) {
				tgt.IncSent()
				tgt.OnSuccess(10*time.Millisecond, 64)
				tgt.OnDuplicate()
				tgt.OnDuplicate()
			},
			want: []string{"+2 duplicates"},
		},
		{
			name: "late replies segment",
			setup: func(tgt *stats.TargetStats) {
				tgt.IncSent()
				tgt.OnFailure("Timeout")
				tgt.OnLateReply()
			},
			want: []string{"1 packets out of wait time"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tgt := stats.NewTargetStats("example.com")
			tgt.SetIP("1.2.3.4")
			tt.setup(tgt)

			var out bytes.Buffer
			printExitSummary(&out, []*stats.TargetStats{tgt})

			s := out.String()
			for _, want := range tt.want {
				if !strings.Contains(s, want) {
					t.Errorf("summary lacks %q: %q", want, s)
				}
			}
			for _, bad := range tt.wantNot {
				if strings.Contains(s, bad) {
					t.Errorf("summary unexpectedly contains %q: %q", bad, s)
				}
			}
		})
	}
}

// TestRunJSONOutput_FinalWrite verifies that a valid JSON snapshot file is
// written when the TUI exits (final snapshot path, no ticker dependency).
func TestRunJSONOutput_FinalWrite(t *testing.T) {
	stubRunSeams(t, func(ui.RunOptions) error { return nil })
	jsonPath := filepath.Join(t.TempDir(), "stats.json")

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
	for _, key := range []string{"timestamp", "targets"} {
		if _, ok := snap[key]; !ok {
			t.Errorf("missing %q in JSON output", key)
		}
	}
}

// TestFinalJSONMatchesJoinedStats checks the final snapshot is taken only
// after the pinger is joined, so it includes replies recorded during the join.
func TestFinalJSONMatchesJoinedStats(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	target.IncSent()
	// The pinger records a reply only when it is joined (Wait), as a worker
	// finishing its last probe would; the snapshot must include it.
	var joinOnce sync.Once
	joining := &fakePinger{onWait: func() { joinOnce.Do(func() { target.OnSuccess(time.Millisecond, 64) }) }}
	sup := newSupervisor(supervisorConfig{makePinger: func(int) pingerController { return joining }, targets: []*stats.TargetStats{target}})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	var errs bytes.Buffer
	finishIteration(config{jsonOutputFile: path}, []*stats.TargetStats{target}, sup, &errs, func() {}, done, func() {}, done)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap stats.ExportSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Targets[0].Recv != target.GetView().Recv {
		t.Fatalf("JSON recv=%d, joined recv=%d", snap.Targets[0].Recv, target.GetView().Recv)
	}
	if snap.Targets[0].Recv != 1 {
		t.Fatalf("JSON recv=%d, want the 1 reply recorded while joining", snap.Targets[0].Recv)
	}
}
