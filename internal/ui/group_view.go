package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// Banner colors for group section headers. Kept distinct from the main
// table header (black background, headerColor text) so a group boundary
// reads as a strong band rather than another data row.
var (
	groupBannerBg = tcell.NewRGBColor(20, 55, 80)
	groupBannerFg = vividCyan
)

// TargetGroup describes a named group of targets for display purposes.
// Indices refer to positions in RunOptions.Targets.
type TargetGroup struct {
	Name    string
	Indices []int
}

// groupTableRowKind classifies each rendered row in the grouped table.
type groupTableRowKind int

const (
	groupRowUngrouped groupTableRowKind = iota // plain target, no group
	groupRowSpacer                             // blank separator row between sections
	groupRowHeader                             // group name banner row
	groupRowSubHeader                          // column header row repeated inside a group section
	groupRowTarget                             // individual target within a group
)

// groupTableRow maps a logical table row to its content source.
type groupTableRow struct {
	kind      groupTableRowKind
	targetIdx int    // index into Targets; -1 for header/spacer/subheader rows
	groupIdx  int    // index into Groups; -1 for ungrouped targets and spacer rows
	groupName string // non-empty for header rows
}

// buildGroupRows returns the ordered list of table rows for the groups
// layout. Ungrouped targets appear first as a flat section (unchanged
// format), then each group renders as a self-contained section: a blank
// spacer row (when something precedes it), a banner header row, a repeated
// column-header row, and finally the group's target rows.
func buildGroupRows(
	targets []*stats.TargetStats,
	groups []TargetGroup,
) []groupTableRow {
	// Build a set of target indices that belong to any group.
	groupedIdx := make(map[int]struct{})
	for _, g := range groups {
		for _, idx := range g.Indices {
			groupedIdx[idx] = struct{}{}
		}
	}

	var rows []groupTableRow

	// Ungrouped targets first, preserving original order.
	for i := range targets {
		if _, inGroup := groupedIdx[i]; inGroup {
			continue
		}
		rows = append(rows, groupTableRow{
			kind: groupRowUngrouped, targetIdx: i, groupIdx: -1,
		})
	}

	// Groups in definition order: [spacer →] banner header → column header → targets.
	for gi, g := range groups {
		if len(rows) > 0 {
			rows = append(rows, groupTableRow{kind: groupRowSpacer, targetIdx: -1, groupIdx: -1})
		}
		rows = append(rows, groupTableRow{
			kind: groupRowHeader, targetIdx: -1, groupIdx: gi, groupName: g.Name,
		})
		rows = append(rows, groupTableRow{kind: groupRowSubHeader, targetIdx: -1, groupIdx: gi})
		for _, idx := range g.Indices {
			if idx >= len(targets) {
				continue
			}
			rows = append(rows, groupTableRow{
				kind: groupRowTarget, targetIdx: idx, groupIdx: gi,
			})
		}
	}

	return rows
}

// setGroupHeaderRow fills a table row with a group banner: a colored band
// spanning every column so the group boundary is visually strong even
// though the table keeps a single set of borders/rules. The label (group
// name + member count) lives in column 0; the last column carries the
// expansion so the band fills any remaining width.
func setGroupHeaderRow(table *tview.Table, tableRow int, colCount int,
	groupName string, memberCount int,
) {
	label := fmt.Sprintf(" ▸ %s  (%d hosts)", groupName, memberCount)

	first := tview.NewTableCell(label).
		SetTextColor(groupBannerFg).
		SetBackgroundColor(groupBannerBg).
		SetAttributes(tcell.AttrBold).
		SetSelectable(false)
	if colCount == 1 {
		first.SetExpansion(1)
	}
	table.SetCell(tableRow, 0, first)
	for c := 1; c < colCount; c++ {
		cell := tview.NewTableCell("").
			SetBackgroundColor(groupBannerBg).
			SetSelectable(false)
		if c == colCount-1 {
			cell.SetExpansion(1)
		}
		table.SetCell(tableRow, c, cell)
	}
}

// setGroupSpacerRow fills a table row with blank, non-selectable cells —
// the visual gap between the ungrouped section / previous group and the
// next group's banner.
func setGroupSpacerRow(table *tview.Table, tableRow int, colCount int) {
	for c := 0; c < colCount; c++ {
		cell := tview.NewTableCell("").
			SetBackgroundColor(tcell.ColorBlack).
			SetSelectable(false)
		if c == colCount-1 {
			cell.SetExpansion(1)
		}
		table.SetCell(tableRow, c, cell)
	}
}

// setHeaderRow fills a table row with the column header cells. Shared by
// the main table header (row 0) and each group section's repeated column
// header, so both stay pixel-for-pixel consistent with the single column
// width calculation for this tick.
func setHeaderRow(table *tview.Table, tableRow int, headers []string, widths []int, aligns []int, color tcell.Color) {
	for i, h := range headers {
		text := formatCellText(h, widths[i], aligns[i])
		cell := tview.NewTableCell(text).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(color).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false).
			SetAlign(aligns[i])
		if i == len(headers)-1 {
			cell.SetExpansion(1)
		}
		table.SetCell(tableRow, i, cell)
	}
}
