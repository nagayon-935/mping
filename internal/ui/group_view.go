package ui

import (
	"fmt"
	"time"

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
	groupRowAggregate                          // worst-case summary for a group
)

// groupTableRow maps a logical table row to its content source.
type groupTableRow struct {
	kind      groupTableRowKind
	targetIdx int    // index into Targets; -1 for header/aggregate
	groupIdx  int    // index into Groups; -1 for ungrouped targets
	groupName string // non-empty for header and aggregate rows
}

// buildGroupRows returns the ordered list of table rows for the groups layout.
// Ungrouped targets appear first, then each group: header → targets → aggregate.
// Individual target rows within collapsed groups are omitted.
func buildGroupRows(
	targets []*stats.TargetStats,
	groups []TargetGroup,
	collapsedGroups map[string]bool,
	filter func(stats.TargetView) bool,
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
	for i, t := range targets {
		if _, inGroup := groupedIdx[i]; inGroup {
			continue
		}
		if filter != nil && !filter(t.GetView()) {
			continue
		}
		rows = append(rows, groupTableRow{
			kind: groupRowUngrouped, targetIdx: i, groupIdx: -1,
		})
	}

	// Groups in definition order: header → [target rows] → aggregate.
	for gi, g := range groups {
		rows = append(rows, groupTableRow{
			kind: groupRowHeader, targetIdx: -1, groupIdx: gi, groupName: g.Name,
		})
		if !collapsedGroups[g.Name] {
			for _, idx := range g.Indices {
				if idx >= len(targets) {
					continue
				}
				rows = append(rows, groupTableRow{
					kind: groupRowTarget, targetIdx: idx, groupIdx: gi,
				})
			}
		}
		rows = append(rows, groupTableRow{
			kind: groupRowAggregate, targetIdx: -1, groupIdx: gi, groupName: g.Name,
		})
	}

	return rows
}

// computeGroupAggregate returns a worst-case TargetView across the given targets.
// Packet counts (Sent, Recv, Loss) are summed; latency metrics take the
// maximum (worst case). MinRTT takes the minimum (best minimum across members).
func computeGroupAggregate(targets []*stats.TargetStats, indices []int) stats.TargetView {
	var agg stats.TargetView
	var minRTTSet bool

	for _, idx := range indices {
		if idx >= len(targets) {
			continue
		}
		v := targets[idx].GetView()
		agg.Sent += v.Sent
		agg.Recv += v.Recv
		agg.Loss += v.Loss

		if v.Recv == 0 {
			continue
		}
		if v.AvgRTT > agg.AvgRTT {
			agg.AvgRTT = v.AvgRTT
		}
		if v.MaxRTT > agg.MaxRTT {
			agg.MaxRTT = v.MaxRTT
		}
		if v.LastRTT > agg.LastRTT {
			agg.LastRTT = v.LastRTT
		}
		if v.Jitter > agg.Jitter {
			agg.Jitter = v.Jitter
		}
		if !minRTTSet || v.MinRTT < agg.MinRTT {
			agg.MinRTT = v.MinRTT
			minRTTSet = true
		}
	}
	return agg
}

// buildAggregateColumns produces table columns for a group aggregate row.
// Column layout matches buildFullColumns so cells align with the header.
func buildAggregateColumns(groupName string, agg stats.TargetView, asnEnabled bool) ([]string, float64) {
	lossRate := calcLossRate(agg)
	cols := []string{
		"",              // Src IP (not applicable)
		"▸ " + groupName, // Dst / group name
	}
	if asnEnabled {
		cols = append(cols, "") // ASN
	}
	cols = append(cols,
		fmt.Sprintf("%d", agg.Recv),
		fmt.Sprintf("%d", agg.Loss),
		fmt.Sprintf("%.1f%%", lossRate),
		formatRTT(agg.LastRTT),
		formatRTT(agg.AvgRTT),
		formatRTT(agg.Jitter),
		"", // Packet size
		"", // MTU
		"", // TTL
		"", // Last error
		"", // Last loss time
	)
	return cols, lossRate
}

// groupHeaderBg is the background colour for group header rows.
var groupHeaderBg = tcell.NewRGBColor(30, 40, 70)

// groupAggregateBg is the background colour for group aggregate rows.
var groupAggregateBg = tcell.NewRGBColor(20, 55, 55)

// setGroupHeaderRow fills a table row with a styled group header.
func setGroupHeaderRow(table *tview.Table, tableRow int, colCount int,
	groupName string, memberCount int, collapsed bool,
) {
	indicator := "▼"
	if collapsed {
		indicator = "▶"
	}
	label := fmt.Sprintf(" %s %s  (%d hosts)", indicator, groupName, memberCount)

	first := tview.NewTableCell(label).
		SetBackgroundColor(groupHeaderBg).
		SetTextColor(tcell.ColorYellow).
		SetAttributes(tcell.AttrBold).
		SetSelectable(false).
		SetExpansion(1)
	table.SetCell(tableRow, 0, first)
	for c := 1; c < colCount; c++ {
		table.SetCell(tableRow, c, tview.NewTableCell("").
			SetBackgroundColor(groupHeaderBg).
			SetSelectable(false))
	}
}

// setGroupAggregateRow fills a table row with worst-case aggregate stats.
func setGroupAggregateRow(
	table *tview.Table, tableRow int,
	groupName string, agg stats.TargetView,
	cols []string, widths []int, aligns []int,
	lossRate float64, asnEnabled bool,
) {
	cells := buildFullRowCells(cols, widths, aligns, lossRate, agg.LastRTT, agg.Jitter,
		tcell.ColorSilver, asnEnabled)
	for c, cell := range cells {
		cell.SetBackgroundColor(groupAggregateBg)
		cell.SetSelectable(false)
		table.SetCell(tableRow, c, cell)
	}
}

// groupHintText returns the footer hint for z-key when groups are defined.
func groupHintText(hasGroups bool) string {
	if hasGroups {
		return " | z: Toggle group collapse"
	}
	return ""
}

// unused import guard — time is used for Duration zero comparisons in tests.
var _ = time.Duration(0)
