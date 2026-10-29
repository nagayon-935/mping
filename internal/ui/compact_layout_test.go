package ui

import (
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
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

func TestBuildCompactLayout(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.OnSuccess(12*time.Millisecond, 64)
	target.SetIfaceMTU(1500)
	layout := buildCompactLayout([]stats.TargetView{target.GetView()}, 56, "10.0.0.2", "", 20)
	if len(layout.rows) != 2 {
		t.Fatalf("rows: got %d", len(layout.rows))
	}
	if len(layout.headers) != 4 || len(layout.aligns) != 4 {
		t.Fatalf("headers/aligns size mismatch")
	}
	if layout.max[3] != 20 {
		t.Fatalf("error max width: got %d", layout.max[3])
	}
}

func TestBuildCompactRowCells(t *testing.T) {
	widths := []int{4, 4, 4, 4}
	aligns := []int{tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignLeft}
	rowColor := tcell.ColorGray

	tests := []struct {
		name      string
		values    []string
		wantTexts []string
		wantColor []tcell.Color
	}{
		{
			name:      "the host is white and a non-blank error is red",
			values:    []string{"host", "path", "stat", "err"},
			wantTexts: []string{"host", "path", "stat", "err "},
			wantColor: []tcell.Color{tcell.ColorWhite, rowColor, rowColor, vividRed},
		},
		{
			name:      "a blank error keeps the row color",
			values:    []string{"host", "path", "stat", "  "},
			wantTexts: []string{"host", "path", "stat", "    "},
			wantColor: []tcell.Color{tcell.ColorWhite, rowColor, rowColor, rowColor},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cells := buildCompactRowCells(tt.values, widths, aligns, rowColor)
			if len(cells) != len(tt.values) {
				t.Fatalf("cells len = %d, want %d", len(cells), len(tt.values))
			}
			for i, cell := range cells {
				if cell.Text != tt.wantTexts[i] {
					t.Errorf("cell %d text = %q, want %q", i, cell.Text, tt.wantTexts[i])
				}
				if got := cellForeground(cell); got != tt.wantColor[i] {
					t.Errorf("cell %d color = %v, want %v", i, got, tt.wantColor[i])
				}
			}
		})
	}
}
