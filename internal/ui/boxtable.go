package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// borderKind selects which corner/junction glyphs a box border line uses.
type borderKind int

const (
	borderTop    borderKind = iota // ┌ ┬ ┐
	borderMid                      // ├ ┼ ┤
	borderBottom                   // └ ┴ ┘
	borderIntro                    // ├ ┬ ┤  (transition from a spanning row into columns)
)

// boxBorder builds a horizontal box-drawing border line for the given column
// widths, wrapped in the standard "[white]…[-]" color tags. No trailing newline
// is added so callers control line termination.
//
// A single-element widths slice yields a plain span border (e.g. "┌────┐"),
// which is how the MTR/HTTP label rows introduce a full-width top edge.
func boxBorder(widths []int, kind borderKind) string {
	var left, mid, right string
	switch kind {
	case borderTop:
		left, mid, right = "┌", "┬", "┐"
	case borderMid:
		left, mid, right = "├", "┼", "┤"
	case borderBottom:
		left, mid, right = "└", "┴", "┘"
	case borderIntro:
		left, mid, right = "├", "┬", "┤"
	}

	var sb strings.Builder
	sb.WriteString("[white]")
	sb.WriteString(left)
	for i, w := range widths {
		if i > 0 {
			sb.WriteString(mid)
		}
		sb.WriteString(strings.Repeat("─", w))
	}
	sb.WriteString(right)
	sb.WriteString("[-]")
	return sb.String()
}

// boxColumn is one column of a box-drawn monitor table. Cells are left-aligned
// unless rightAlign is set. Both alignments reserve a space at each edge.
type boxColumn struct {
	header     string
	width      int
	rightAlign bool
}

func (c boxColumn) pad(text string) string {
	if c.rightAlign {
		return rightPaddedCell(text, c.width)
	}
	return paddedCell(text, c.width)
}

// boxCell is one data cell. A non-empty tag colours it (status, loss, RTT);
// other cells are white.
type boxCell struct{ text, tag string }

func plainCells(texts ...string) []boxCell {
	cells := make([]boxCell, len(texts))
	for i, t := range texts {
		cells[i] = boxCell{text: t}
	}
	return cells
}

func boxWidths(cols []boxColumn) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = c.width
	}
	return widths
}

// boxInnerWidth is the width between the outer borders: the columns plus
// the separators between them.
func boxInnerWidth(cols []boxColumn) int {
	w := len(cols) - 1
	for _, c := range cols {
		w += c.width
	}
	return w
}

// boxHeaderRow builds a bold-yellow header row ("│ h1 │ h2 │ …"). No
// trailing newline is added.
func boxHeaderRow(cols []boxColumn) string {
	var sb strings.Builder
	sb.WriteString("[white]│")
	for _, c := range cols {
		sb.WriteString("[yellow::b]")
		sb.WriteString(c.pad(c.header))
		sb.WriteString("[white::-]│")
	}
	sb.WriteString("[-]")
	return sb.String()
}

// boxRow builds one data row. No trailing newline is added.
func boxRow(cols []boxColumn, cells []boxCell) string {
	var sb strings.Builder
	sb.WriteString("[white]│")
	for i, c := range cols {
		if tag := cells[i].tag; tag != "" {
			sb.WriteString(tag + tview.Escape(c.pad(cells[i].text)) + "[-][white]│")
		} else {
			sb.WriteString("[white]" + tview.Escape(c.pad(cells[i].text)) + "[white]│")
		}
	}
	sb.WriteString("[-]")
	return sb.String()
}

// boxSpanRow draws a padded row spanning all columns.
func boxSpanRow(text string, innerW int, colorTag string) string {
	return "[white]│" + colorTag + tview.Escape(paddedCell(text, innerW)) + "[white::-]│[-]"
}

// writeLabelledBoxTable writes a table with a spanning target label above
// the column headings.
func writeLabelledBoxTable(sb *strings.Builder, cols []boxColumn, label string, groups [][][]boxCell, empty string) {
	widths := boxWidths(cols)
	innerW := boxInnerWidth(cols)
	fmt.Fprintln(sb, boxBorder([]int{innerW}, borderTop))
	fmt.Fprintln(sb, boxSpanRow(label, innerW, "[yellow::b]"))
	fmt.Fprintln(sb, boxBorder(widths, borderIntro))
	writeBoxBody(sb, cols, groups, empty)
}

// writeBoxTable writes a table with a plain top border: header, rows (a
// rule between groups of rows) and the bottom border. A table with no
// groups shows empty instead, unless empty is "".
func writeBoxTable(sb *strings.Builder, cols []boxColumn, groups [][][]boxCell, empty string) {
	fmt.Fprintln(sb, boxBorder(boxWidths(cols), borderTop))
	writeBoxBody(sb, cols, groups, empty)
}

// writeBoxBody writes what follows a table's top edge: the header, the rows
// (a rule between groups of rows), the empty-table placeholder when there are
// no groups and empty is not "", and the bottom border.
func writeBoxBody(sb *strings.Builder, cols []boxColumn, groups [][][]boxCell, empty string) {
	widths := boxWidths(cols)
	fmt.Fprintln(sb, boxHeaderRow(cols))
	fmt.Fprintln(sb, boxBorder(widths, borderMid))
	for i, rows := range groups {
		for _, cells := range rows {
			fmt.Fprintln(sb, boxRow(cols, cells))
		}
		if i < len(groups)-1 {
			fmt.Fprintln(sb, boxBorder(widths, borderMid))
		}
	}
	if len(groups) == 0 && empty != "" {
		fmt.Fprintln(sb, boxSpanRow(empty, boxInnerWidth(cols), levelTag(stats.LevelPending)))
	}
	fmt.Fprintln(sb, boxBorder(widths, borderBottom))
}

// monitorColumn is a box-table column together with the cell it shows for one
// value of type V (a hop, an HTTP check, a port result, ...). A full column is
// dropped from the compact layout.
type monitorColumn[V any] struct {
	boxColumn
	cell func(V) boxCell
	full bool // optional: dropped (rightmost first) when the pane is too narrow
}

// layoutColumns returns the columns that fit availW at their declared
// widths, in order, together with their box-table columns. Optional (full)
// columns are dropped one at a time from the right until the rest fit, so a
// medium-width pane keeps as many columns as it can instead of jumping
// straight to the compact set.
func layoutColumns[V any](all []monitorColumn[V], availW int) ([]monitorColumn[V], []boxColumn) {
	shown := make([]bool, len(all))
	width := 1 // the left border; each column adds its width and a separator
	for i, c := range all {
		shown[i] = true
		width += c.width + 1
	}
	for i := len(all) - 1; i >= 0 && width > availW; i-- {
		if all[i].full {
			shown[i] = false
			width -= all[i].width + 1
		}
	}
	var cols []monitorColumn[V]
	var boxCols []boxColumn
	for i, c := range all {
		if shown[i] {
			cols = append(cols, c)
			boxCols = append(boxCols, c.boxColumn)
		}
	}
	return cols, boxCols
}

// fitMonitorColumns sizes the table from the declared widths. Fixed columns
// get proportional shares of spare width, and the flexible column absorbs
// the remaining space or shrinks when even the minimal layout is too wide.
func fitMonitorColumns[V any](all []monitorColumn[V], flexibleHeader string, availW int) ([]monitorColumn[V], []boxColumn) {
	cols, boxCols := layoutColumns(all, availW)
	spare := availW - boxInnerWidth(boxCols) - 2
	total, flex := 0, -1
	for i, c := range boxCols {
		total += c.width
		if c.header == flexibleHeader {
			flex = i
		}
	}
	if flex < 0 || boxCols[flex].width+spare < 2 {
		return nil, nil
	}
	remaining := spare
	if spare > 0 {
		for i := range boxCols {
			if i != flex {
				extra := spare * boxCols[i].width / total
				boxCols[i].width += extra
				remaining -= extra
			}
		}
	}
	boxCols[flex].width += remaining
	for i := range cols {
		cols[i].width = boxCols[i].width
	}
	return cols, boxCols
}

const monitorTooNarrow = "Pane too narrow"

// rowCells builds the table cells of cols for v.
func rowCells[V any](cols []monitorColumn[V], v V) []boxCell {
	cells := make([]boxCell, len(cols))
	for i, c := range cols {
		cells[i] = c.cell(v)
	}
	return cells
}

// plainCell shows text(v) uncoloured.
func plainCell[V any](text func(V) string) func(V) boxCell {
	return func(v V) boxCell { return boxCell{text: text(v)} }
}

// rttCell displays milliseconds in the color chosen by classify.
func rttCell[V any](get func(V) time.Duration, classify func(time.Duration) stats.Level) func(V) boxCell {
	return func(v V) boxCell {
		d := get(v)
		return boxCell{text: formatFixedMs(d), tag: rttColorTag(classify(d))}
	}
}
