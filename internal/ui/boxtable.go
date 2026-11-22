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
// with a leading space (paddedCell) unless rightAlign is set, which right-aligns
// them with a trailing space (rightPaddedCell).
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
		sb.WriteString("[white]│")
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
			sb.WriteString(tag + c.pad(cells[i].text) + "[-][white]│")
		} else {
			sb.WriteString("[white]" + c.pad(cells[i].text) + "[white]│")
		}
	}
	sb.WriteString("[-]")
	return sb.String()
}

// boxSpanRow builds a single full-width row spanning innerW columns, used for
// label/placeholder lines such as " Discovering..." or " Waiting for results...".
// colorTag is the color applied to the text (e.g. "[darkgray]" or "[yellow::b]").
// No trailing newline is added.
func boxSpanRow(text string, innerW int, colorTag string) string {
	return "[white]│" + colorTag + formatCellText(text, innerW, tview.AlignLeft) + "[white]│[-]"
}

// writeLabelledBoxTable writes a table under a full-width top edge: an
// optional bold label row, the header, rows (a rule between groups of rows)
// and the bottom border. A table with no groups shows empty instead.
func writeLabelledBoxTable(sb *strings.Builder, cols []boxColumn, label string, groups [][][]boxCell, empty string) {
	widths := boxWidths(cols)
	innerW := boxInnerWidth(cols)
	fmt.Fprintln(sb, boxBorder([]int{innerW}, borderTop))
	if label != "" {
		fmt.Fprintln(sb, boxSpanRow(" "+label, innerW, "[yellow::b]"))
	}
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
		fmt.Fprintln(sb, boxSpanRow(empty, boxInnerWidth(cols), "[darkgray]"))
	}
	fmt.Fprintln(sb, boxBorder(widths, borderBottom))
}

// monitorColumn is a box-table column together with the cell it shows for one
// value of type V (a hop, an HTTP check, a port result, ...). A full column is
// dropped from the compact layout.
type monitorColumn[V any] struct {
	boxColumn
	cell func(V) boxCell
	full bool // shown only in the full layout
}

// layoutColumns returns the columns shown in the given layout, in order,
// together with their box-table columns.
func layoutColumns[V any](all []monitorColumn[V], compact bool) ([]monitorColumn[V], []boxColumn) {
	var cols []monitorColumn[V]
	var boxCols []boxColumn
	for _, c := range all {
		if !c.full || !compact {
			cols = append(cols, c)
			boxCols = append(boxCols, c.boxColumn)
		}
	}
	return cols, boxCols
}

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

// rttCell shows the duration get(v) formatted by format and coloured by the
// level classify assigns it.
func rttCell[V any](get func(V) time.Duration, format func(time.Duration) string, classify func(time.Duration) stats.Level) func(V) boxCell {
	return func(v V) boxCell {
		d := get(v)
		return boxCell{text: format(d), tag: rttColorTag(classify(d))}
	}
}
