package ui

import (
	"testing"
)

func TestRightPaddedCell(t *testing.T) {
	tests := []struct {
		text string
		colW int
		want string
	}{
		{"abc", 5, " abc "}, // right-aligned "abc " in 5 chars: " abc "
		{"", 3, "   "},    // empty string
	}

	for _, tt := range tests {
		got := rightPaddedCell(tt.text, tt.colW)
		if got != tt.want {
			t.Errorf("rightPaddedCell(%q, %d): got %q, want %q", tt.text, tt.colW, got, tt.want)
		}
	}
}
