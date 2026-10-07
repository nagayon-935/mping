package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
)

func (tr *tableRenderer) selectedTarget() *stats.TargetStats {
	for _, t := range tr.targets {
		if t.ID == tr.selectedID {
			return t
		}
	}
	return nil
}

func (tr *tableRenderer) selectedIndex() int {
	for i, t := range tr.targets {
		if t.ID == tr.selectedID {
			return i
		}
	}
	return -1
}

func (tr *tableRenderer) selectedRow() int {
	i := tr.selectedIndex()
	if i < 0 {
		return -1
	}
	if tr.compactLayout {
		return 1 + i*2
	}
	if len(tr.groups) > 0 {
		for r, row := range tr.groupRowMap {
			if row.targetIdx == i {
				return r + 1
			}
		}
		return -1
	}
	return i + 1
}

func (tr *tableRenderer) reconcileSelection() {
	if tr.selectedTarget() == nil && len(tr.targets) > 0 {
		tr.selectedID = tr.targets[0].ID
		tr.scrollToSelection = true
	}
	offset, col := tr.table.GetOffset()
	_, _, _, height := tr.table.GetInnerRect()
	visible := max(1, (height-3)/2) // Table borders consume one line per row.
	if tr.scrollToSelection {
		row := tr.selectedRow() - 1
		if row < offset {
			offset = row
		}
		if row >= offset+visible {
			offset = row - visible + 1
		}
		tr.scrollToSelection = false
	}
	offset = max(0, min(offset, max(0, tr.rowCount-1-visible)))
	tr.table.SetOffset(offset, col)
}

func (tr *tableRenderer) moveSelection(key tcell.Key) {
	if tr.beforeUpdate != nil {
		tr.beforeUpdate()
	}
	if len(tr.targets) == 0 {
		return
	}
	order := make([]int, 0, len(tr.targets))
	if len(tr.groups) > 0 && !tr.compactLayout {
		for _, row := range buildGroupRows(tr.targets, tr.groups) {
			if row.targetIdx >= 0 {
				order = append(order, row.targetIdx)
			}
		}
	} else {
		for i := range tr.targets {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		return
	}
	i := 0
	for n, index := range order {
		if tr.targets[index].ID == tr.selectedID {
			i = n
			break
		}
	}
	delta := 1
	switch key {
	case tcell.KeyUp:
		delta = -1
	case tcell.KeyPgUp:
		delta = -tableMaxRows
	case tcell.KeyPgDn:
		delta = tableMaxRows
	}
	i = max(0, min(len(order)-1, i+delta))
	tr.selectedID = tr.targets[order[i]].ID
	tr.scrollToSelection = true
	tr.update()
}

func (tr *tableRenderer) highlightSelection() {
	row := tr.selectedRow()
	if row < 1 {
		return
	}
	rows := 1
	if tr.compactLayout {
		rows = 2
	}
	for r := row; r < row+rows; r++ {
		for c := range tr.activeHeaders {
			cell := tr.table.GetCell(r, c)
			if cell.Text != "" {
				cell.SetBackgroundColor(tcell.NewRGBColor(25, 50, 70))
			}
		}
	}
}
