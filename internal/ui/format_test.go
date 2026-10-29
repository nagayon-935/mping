package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/rivo/tview"
)

func TestFormatCellText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		align int
		want  string
	}{
		{"left alignment truncates with an ellipsis", "abcdefghijk", 8, tview.AlignLeft, "abcde..."},
		{"right alignment pads on the left", "42", 5, tview.AlignRight, "   42"},
		{"left alignment pads on the right", "hi", 5, tview.AlignLeft, "hi   "},
		{"width below the ellipsis keeps only dots", "abcd", 3, tview.AlignLeft, "..."},
		{"zero width renders nothing", "x", 0, tview.AlignLeft, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCellText(tt.text, tt.width, tt.align); got != tt.want {
				t.Errorf("formatCellText(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
			}
		})
	}
}

func TestTruncateToDisplayWidth(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"long string gets an ellipsis", "abcdef", 4, "a..."},
		{"string that fits is unchanged", "ab", 2, "ab"},
		{"width 2 keeps two dots", "hello", 2, ".."},
		{"width 1 keeps one dot", "hello", 1, "."},
		{"width 0 is empty", "hello", 0, ""},
		{"negative width is empty", "hello", -1, ""},
		// U+0300 (combining grave accent) has runewidth 0, which the code
		// counts as 1 column, so "̀hello" is 6 wide and width 4 leaves 1
		// column for content before the ellipsis.
		{"zero-width rune counts as one column", "̀hello", 4, "̀..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateToDisplayWidth(tt.in, tt.width); got != tt.want {
				t.Errorf("truncateToDisplayWidth(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}

func TestCalcLossRate(t *testing.T) {
	tests := []struct {
		name string
		view stats.TargetView
		want float64
	}{
		{"a fifth lost", stats.TargetView{Recv: 80, Loss: 20}, 20},
		{"nothing sent yet", stats.TargetView{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calcLossRate(tt.view); got < tt.want-0.1 || got > tt.want+0.1 {
				t.Errorf("calcLossRate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatLossAgo(t *testing.T) {
	if got := formatLossAgo(time.Time{}); got != "-" {
		t.Fatalf("zero time: got %q", got)
	}
	got := formatLossAgo(time.Now().Add(-2 * time.Second))
	if !strings.Contains(got, "ago") {
		t.Fatalf("expected ago suffix: got %q", got)
	}
}

func TestIntOrDash(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "-"},
		{-1, "-"},
		{64, "64"},
		{1500, "1500"},
	}
	for _, tt := range tests {
		if got := intOrDash(tt.in); got != tt.want {
			t.Errorf("intOrDash(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatRTT(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "-"},
		{10 * time.Millisecond, "10ms"},
	}
	for _, tt := range tests {
		if got := formatRTT(tt.in); got != tt.want {
			t.Errorf("formatRTT(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPaddedCells(t *testing.T) {
	tests := []struct {
		name string
		fn   func(text string, colW int) string
		text string
		colW int
		want string
	}{
		{"paddedCell adds a leading space and pads to the width", paddedCell, "abc", 10, " abc      "},
		{"paddedCell of empty text is blank", paddedCell, "", 5, "     "},
		{"rightPaddedCell adds a trailing space and pads on the left", rightPaddedCell, "abc", 5, " abc "},
		{"rightPaddedCell of empty text is blank", rightPaddedCell, "", 3, "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(tt.text, tt.colW); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
