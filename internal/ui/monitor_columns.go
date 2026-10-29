package ui

import "time"

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

// rttCell shows the duration get(v) formatted by format and coloured by RTT
// threshold.
func rttCell[V any](get func(V) time.Duration, format func(time.Duration) string) func(V) boxCell {
	return func(v V) boxCell {
		d := get(v)
		return boxCell{text: format(d), tag: mtrRTTColorTag(d)}
	}
}
