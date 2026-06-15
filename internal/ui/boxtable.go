package ui

import (
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

// boxHeaderRow builds a bold-yellow header row ("│ h1 │ h2 │ …") where every
// cell is center-padded to its column width. No trailing newline is added.
func boxHeaderRow(headers []string, widths []int) string {
	var sb strings.Builder
	sb.WriteString("[white]│")
	for i, h := range headers {
		sb.WriteString("[yellow::b]")
		sb.WriteString(paddedCell(h, widths[i]))
		sb.WriteString("[white]│")
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
