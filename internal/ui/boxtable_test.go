package ui

import "testing"

func TestBoxBorder(t *testing.T) {
	tests := []struct {
		name   string
		widths []int
		kind   borderKind
		want   string
	}{
		{"top single span", []int{4}, borderTop, "[white]┌────┐[-]"},
		{"top multi", []int{2, 3}, borderTop, "[white]┌──┬───┐[-]"},
		{"mid multi", []int{2, 3}, borderMid, "[white]├──┼───┤[-]"},
		{"bottom multi", []int{2, 3}, borderBottom, "[white]└──┴───┘[-]"},
		{"intro multi", []int{2, 3}, borderIntro, "[white]├──┬───┤[-]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boxBorder(tt.widths, tt.kind); got != tt.want {
				t.Errorf("boxBorder(%v, %v) = %q, want %q", tt.widths, tt.kind, got, tt.want)
			}
		})
	}
}

func TestBoxHeaderRow(t *testing.T) {
	// paddedCell adds a leading space then right-pads to colW.
	got := boxHeaderRow([]string{"A", "BB"}, []int{4, 5})
	want := "[white]│[yellow::b]" + paddedCell("A", 4) + "[white]│[yellow::b]" + paddedCell("BB", 5) + "[white]│[-]"
	if got != want {
		t.Errorf("boxHeaderRow = %q, want %q", got, want)
	}
}

func TestBoxSpanRow(t *testing.T) {
	got := boxSpanRow(" Waiting...", 20, "[darkgray]")
	want := "[white]│[darkgray]" + formatCellTextLeft(" Waiting...", 20) + "[white]│[-]"
	if got != want {
		t.Errorf("boxSpanRow = %q, want %q", got, want)
	}
}

// formatCellTextLeft mirrors how boxSpanRow left-aligns text, kept local to the
// test so the expectation tracks formatCellText's left-alignment behavior.
func formatCellTextLeft(text string, width int) string {
	return formatCellText(text, width, 0) // tview.AlignLeft == 0
}
