package ui

import (
	"testing"

	"github.com/nagayon-935/mping/internal/stats"
)

func TestFormatTableError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "extract tail message",
			in:   "write ip 10.70.71.68->8.8.8.8: sendmsg: no route to host",
			want: "no route to host",
		},
		{
			name: "no separator keeps original",
			in:   "Timeout",
			want: "Timeout",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTableError(tt.in); got != tt.want {
				t.Fatalf("formatTableError(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCalcInitialTableErrorWidth(t *testing.T) {
	failed := stats.NewTargetStats("example.com")
	failed.OnFailure("This is a very long error message that exceeds normal width expectations!!")
	tests := []struct {
		name        string
		targets     []*stats.TargetStats
		minWidth    int
		wantAtLeast int
	}{
		{
			name: "includes the known error candidates", minWidth: 10,
			wantAtLeast: len("Communication Administratively Prohibited"),
		},
		{
			name: "includes a target's current error", targets: []*stats.TargetStats{failed}, minWidth: 5,
			wantAtLeast: len("This is a very long error message"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calcInitialTableErrorWidth(tt.targets, "Error", tt.minWidth); got < tt.wantAtLeast {
				t.Errorf("calcInitialTableErrorWidth = %d, want at least %d", got, tt.wantAtLeast)
			}
		})
	}
}
