package ui

import (
	"slices"
	"testing"
)

func TestFitMonitorColumns(t *testing.T) {
	all := []monitorColumn[int]{
		{boxColumn: boxColumn{header: "Extra", width: 7}, full: true},
		{boxColumn: boxColumn{header: "Flexible", width: 10}},
		{boxColumn: boxColumn{header: "Last", width: 6}},
	}
	for _, tt := range []struct {
		name    string
		avail   int
		content int
		want    []int
	}{
		{"full leaves spare width", 32, 12, []int{7, 12, 6}},
		{"full exact fit", 27, 20, []int{7, 10, 6}},
		{"compact with spare width", 26, 14, []int{14, 6}},
		{"compact shrinks below minimum", 15, 14, []int{6, 6}},
		{"compact uses minimum for short content", 26, 5, []int{10, 6}},
		{"compact cannot fit its fixed columns", 10, 14, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cols, boxCols := fitMonitorColumns(all, "Flexible", tt.content, tt.avail)
			if got := boxWidths(boxCols); !slices.Equal(got, tt.want) {
				t.Errorf("box widths = %v, want %v", got, tt.want)
			}
			for i, col := range cols {
				if col.boxColumn != boxCols[i] {
					t.Errorf("column %d = %+v, want %+v", i, col.boxColumn, boxCols[i])
				}
			}
		})
	}
}

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
	got := boxHeaderRow([]boxColumn{{header: "A", width: 4}, {header: "BB", width: 5}})
	want := "[white]│[yellow::b]" + paddedCell("A", 4) + "[white::-]│[yellow::b]" + paddedCell("BB", 5) + "[white::-]│[-]"
	if got != want {
		t.Errorf("boxHeaderRow = %q, want %q", got, want)
	}
}

func TestBoxRowColoursOnlyTaggedCells(t *testing.T) {
	cols := []boxColumn{{width: 4, rightAlign: true}, {width: 6}}
	got := boxRow(cols, []boxCell{{text: "a"}, {text: "Open", tag: "[green]"}})
	want := "[white]│[white]" + rightPaddedCell("a", 4) + "[white]│[green]" + paddedCell("Open", 6) + "[-][white]│[-]"
	if got != want {
		t.Errorf("boxRow = %q, want %q", got, want)
	}
}

func TestBoxSpanRow(t *testing.T) {
	got := boxSpanRow("Discovering...", 17, "[gray]")
	want := "[white]│[gray] Discovering...  [white::-]│[-]"
	if got != want {
		t.Errorf("boxSpanRow = %q, want %q", got, want)
	}
}
