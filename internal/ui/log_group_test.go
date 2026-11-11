package ui

import (
	"slices"
	"testing"

	"github.com/rivo/tview"
)

func TestAppendLog_GroupsSameCauseInOneSecond(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name: "same cause, same second: one line naming every host",
			lines: []string{
				"[12:09:03] 1.1.1.1: crit: RTT 679.8ms > 200.0ms · Jitter 43.3ms > 10.0ms",
				"[12:09:03] 8.8.8.8: crit: RTT 679.9ms > 200.0ms · Jitter 43.4ms > 10.0ms",
				"[12:09:03] 9.9.9.9: crit: RTT 679.9ms > 200.0ms · Jitter 43.1ms > 10.0ms",
			},
			want: []string{"[12:09:03] crit: RTT > 200.0ms · Jitter > 10.0ms ×3 — 1.1.1.1, 8.8.8.8, 9.9.9.9"},
		},
		{
			name: "a single host keeps its measured values",
			lines: []string{
				"[12:09:03] 1.1.1.1: warn: Jitter 48.1ms > 10.0ms",
			},
			want: []string{"[12:09:03] 1.1.1.1: warn: Jitter 48.1ms > 10.0ms"},
		},
		{
			name: "different status, cause or second stay separate",
			lines: []string{
				"[12:09:03] a: warn: Jitter 48.1ms > 10.0ms",
				"[12:09:03] b: crit: Jitter 50.5ms > 50.0ms",
				"[12:09:04] c: crit: Jitter 50.6ms > 50.0ms",
				"[12:09:04] d: crit: RTT 250.0ms > 200.0ms",
			},
			want: []string{
				"[12:09:03] a: warn: Jitter 48.1ms > 10.0ms",
				"[12:09:03] b: crit: Jitter 50.5ms > 50.0ms",
				"[12:09:04] c: crit: Jitter 50.6ms > 50.0ms",
				"[12:09:04] d: crit: RTT 250.0ms > 200.0ms",
			},
		},
		{
			name: "another line in between ends the group",
			lines: []string{
				"[12:09:03] a: crit: 3 consecutive failures",
				"[12:09:03] Reloaded hosts file",
				"[12:09:03] b: crit: 3 consecutive failures",
			},
			want: []string{
				"[12:09:03] a: crit: 3 consecutive failures",
				"[12:09:03] Reloaded hosts file",
				"[12:09:03] b: crit: 3 consecutive failures",
			},
		},
		{
			name: "duplicate reports from the same host in the same second are deduplicated",
			lines: []string{
				"[12:00:00] h1: ok: Within thresholds",
				"[12:00:00] h1: ok: Within thresholds",
			},
			want: []string{"[12:00:00] h1: ok: Within thresholds"},
		},
		{
			name: "long groups name the first hosts and count the rest",
			lines: []string{
				"[12:00:00] h1: ok: Within thresholds", "[12:00:00] h2: ok: Within thresholds",
				"[12:00:00] h3: ok: Within thresholds", "[12:00:00] h4: ok: Within thresholds",
				"[12:00:00] h5: ok: Within thresholds", "[12:00:00] h6: ok: Within thresholds",
			},
			want: []string{"[12:00:00] ok: Within thresholds ×6 — h1, h2, h3, h4 +2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			vs := newViewState(tview.NewTextView())

			// Act
			for _, line := range tt.lines {
				vs.appendLog(line)
			}

			// Assert
			if !slices.Equal(vs.errorLogs, tt.want) {
				t.Errorf("log =\n %q\nwant\n %q", vs.errorLogs, tt.want)
			}
		})
	}
}

func TestReset_EndsLogGroup(t *testing.T) {
	vs := newViewState(tview.NewTextView())
	vs.appendLog("[12:09:03] a: crit: Jitter 50.5ms > 50.0ms")
	vs.reset()
	vs.appendLog("[12:09:03] b: crit: Jitter 50.6ms > 50.0ms")
	if want := []string{"[12:09:03] b: crit: Jitter 50.6ms > 50.0ms"}; !slices.Equal(vs.errorLogs, want) {
		t.Errorf("log = %q, want %q", vs.errorLogs, want)
	}
}
