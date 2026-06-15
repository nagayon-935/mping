package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
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
	groupRowHeader                             // group name separator row
	groupRowTarget                             // individual target within a group
)

// groupTableRow maps a logical table row to its content source.
type groupTableRow struct {
	kind      groupTableRowKind
	targetIdx int    // index into Targets; -1 for header
	groupIdx  int    // index into Groups; -1 for ungrouped targets
	groupName string // non-empty for header rows
}

// buildGroupRows returns the ordered list of table rows for the groups layout.
// Ungrouped targets appear first, then each group: header → targets.
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

	// Groups in definition order: header → target rows.
	for gi, g := range groups {
		rows = append(rows, groupTableRow{
			kind: groupRowHeader, targetIdx: -1, groupIdx: gi, groupName: g.Name,
		})
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

// setGroupHeaderRow fills a table row with a group header separator.
func setGroupHeaderRow(table *tview.Table, tableRow int, colCount int,
	groupName string, memberCount int,
) {
	label := fmt.Sprintf(" ▸ %s  (%d hosts)", groupName, memberCount)

	first := tview.NewTableCell(label).
		SetAttributes(tcell.AttrBold).
		SetSelectable(false).
		SetExpansion(1)
	table.SetCell(tableRow, 0, first)
	for c := 1; c < colCount; c++ {
		table.SetCell(tableRow, c, tview.NewTableCell("").SetSelectable(false))
	}
}
