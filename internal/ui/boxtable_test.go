package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/rivo/tview"
)

func assertBoxTableWidth(t *testing.T, table string, want int) {
	t.Helper()
	for line := range strings.SplitSeq(strings.TrimSuffix(table, "\n"), "\n") {
		if got := tview.TaggedStringWidth(line); got != want {
			t.Errorf("table line spans %d cells, want %d: %q", got, want, line)
		}
	}
}

func TestFitMonitorColumns(t *testing.T) {
	all := []monitorColumn[int]{
		{boxColumn: boxColumn{header: "Extra", width: 7}, full: true},
		{boxColumn: boxColumn{header: "Flexible", width: 10}},
		{boxColumn: boxColumn{header: "Last", width: 6}},
	}
	for _, tt := range []struct {
		name  string
		avail int
		want  []int
	}{
		{"wider column gets more extra", 50, []int{14, 20, 12}},
		{"full exact fit", 27, []int{7, 10, 6}},
		{"compact distributes spare", 26, []int{15, 8}},
		{"compact shrinks below minimum", 15, []int{6, 6}},
		{"compact cannot fit its fixed columns", 10, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cols, boxCols := fitMonitorColumns(all, "Flexible", tt.avail)
			if got := boxWidths(boxCols); !slices.Equal(got, tt.want) {
				t.Errorf("box widths = %v, want %v", got, tt.want)
			}
			if len(cols) != len(boxCols) {
				t.Fatalf("got %d monitor columns and %d box columns", len(cols), len(boxCols))
			}
			if len(boxCols) > 0 {
				if got := boxInnerWidth(boxCols) + 2; got != tt.avail {
					t.Errorf("table spans %d cells, want %d", got, tt.avail)
				}
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
	cols := []boxColumn{{width: 4, rightAlign: true}, {width: 9}}
	var row strings.Builder
	writeBoxRow(&row, cols, []boxCell{{text: "a"}, {text: "界[red]", tag: "[green]"}})
	want := "[white]│[white]  a [white]│[green] 界[red[] [-][white]│[-]"
	if got := row.String(); got != want {
		t.Errorf("box row = %q, want %q", got, want)
	}
	assertBoxTableWidth(t, row.String(), 16)
}

func TestBoxSpanRow(t *testing.T) {
	got := boxSpanRow("Discovering...", 17, "[gray]")
	want := "[white]│[gray] Discovering...  [white::-]│[-]"
	if got != want {
		t.Errorf("boxSpanRow = %q, want %q", got, want)
	}
}
