package ui

import (
	"fmt"
	"strings"

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

// boxColumn is one column of a box-drawn monitor table. Cells are centred
// (paddedCell) unless leftAlign is set (rightPaddedCell).
type boxColumn struct {
	header    string
	width     int
	leftAlign bool
}

func (c boxColumn) pad(text string) string {
	if c.leftAlign {
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
	if len(groups) == 0 {
		fmt.Fprintln(sb, boxSpanRow(empty, innerW, "[darkgray]"))
	}
	fmt.Fprintln(sb, boxBorder(widths, borderBottom))
}

// writeBoxTable writes a table with a plain top border: header, rows (a
// rule between groups of rows) and the bottom border. A table with no
// groups shows empty instead.
func writeBoxTable(sb *strings.Builder, cols []boxColumn, groups [][][]boxCell, empty string) {
	widths := boxWidths(cols)
	fmt.Fprintln(sb, boxBorder(widths, borderTop))
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
