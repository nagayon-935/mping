package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

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
