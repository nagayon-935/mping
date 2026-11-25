package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mattn/go-runewidth"
)

// columnPlan is the full layout chosen for one tick: which columns are shown
// (indices into the schema, in display order), their widths, and which
// columns were hidden for width, in the order they were hidden. ok is false
// when even the core columns do not fit.
type columnPlan struct {
	active []int
	widths []int
	hidden []string
	ok     bool
}

// calcColumnWidths returns the reference content widths. Only identity
// columns measure row text, so changes to probe results cannot resize the table.
func (tr *tableRenderer) calcColumnWidths(texts [][]string) []int {
	widths := make([]int, len(tr.cols))
	for i, c := range tr.cols {
		widths[i] = c.base
		if c.dynamic && !c.fixed {
			widths[i] = runewidth.StringWidth(c.name)
			for _, row := range texts {
				widths[i] = max(widths[i], runewidth.StringWidth(row[i]))
			}
		}
	}
	return widths
}

// planColumns fits padded cells and separators into the Ping pane's inner
// width, hiding columns in rank order until their reference widths fit.
func (tr *tableRenderer) planColumns(texts [][]string, availableTableWidth int) columnPlan {
	references := tr.calcColumnWidths(texts)
	var active, hideOrder []int
	for i, c := range tr.cols {
		if c.hideWhenEmpty && !slices.ContainsFunc(texts, func(row []string) bool {
			return row[i] != "" && row[i] != "-"
		}) {
			continue
		}
		active = append(active, i)
		if c.hideRank > 0 {
			hideOrder = append(hideOrder, i)
		}
	}
	slices.SortStableFunc(hideOrder, func(a, b int) int { return tr.cols[a].hideRank - tr.cols[b].hideRank })

	var hidden []string
	for next := 0; ; next++ {
		if widths, ok := tr.fitColumns(active, references, availableTableWidth, false); ok {
			return columnPlan{active: active, widths: widths, hidden: hidden, ok: true}
		}
		if next == len(hideOrder) {
			break
		}
		drop := hideOrder[next]
		active = slices.DeleteFunc(active, func(i int) bool { return i == drop })
		hidden = append(hidden, tr.cols[drop].name)
	}

	// All optional columns are gone. Give the core identity text whatever
	// room remains after reserving the numeric widths and padding.
	widths, ok := tr.fitColumns(active, references, availableTableWidth, true)
	return columnPlan{active: active, widths: widths, hidden: hidden, ok: ok}
}

func (tr *tableRenderer) fitColumns(active, references []int, availableTableWidth int, allowTruncation bool) ([]int, bool) {
	spare := availableTableWidth - max(len(active)-1, 0) - boxCellPadding*len(active)
	for _, i := range active {
		spare -= references[i]
	}
	if spare < 0 && !allowTruncation {
		return nil, false
	}

	widths := make([]int, len(active))
	for j, i := range active {
		widths[j] = references[i] + boxCellPadding
	}
	if spare >= 0 {
		weights := make([]int, len(active))
		for j, i := range active {
			weights[j] = references[i]
		}
		spreadProportionally(widths, weights, spare)
		return widths, true
	}
	for j, i := range active {
		if c := tr.cols[i]; c.dynamic {
			minimum := max(c.min, runewidth.StringWidth(c.name))
			cut := min(-spare, max(references[i]-minimum, 0))
			widths[j] -= cut
			spare += cut
		}
	}
	return widths, spare == 0
}

// spreadProportionally adds positive extra width in proportion to the
// weights. Zero-weight columns are skipped, and the first eligible columns
// get one cell each from rounding.
func spreadProportionally(widths, weights []int, extra int) {
	if extra <= 0 {
		return
	}
	total := 0
	for _, w := range weights {
		total += w
	}
	if total == 0 {
		return
	}
	remaining := extra
	for i, w := range weights {
		share := extra * w / total
		widths[i] += share
		remaining -= share
	}
	for i, w := range weights {
		if remaining == 0 {
			break
		}
		if w > 0 {
			widths[i]++
			remaining--
		}
	}
}

// paneTitle names the Ping Monitor pane and, when columns are hidden for
// width, says which: a hidden setting column whose value every row shares
// (the payload size, a pinned source address) is shown with that value, the
// rest by name. When that does not fit in maxWidth it falls back to the hidden
// names alone, then to a count.
func (tr *tableRenderer) paneTitle(texts [][]string, maxWidth int) string {
	const plain = " Ping Monitor "
	if len(tr.hidden) == 0 {
		return plain
	}
	var shared, names []string
	for _, name := range tr.hidden {
		i := columnIndex(tr.cols, name)
		if v, ok := sharedValue(texts, i); ok && tr.cols[i].setting {
			shared = append(shared, fmt.Sprintf("%s %s", name, v))
		} else {
			names = append(names, name)
		}
	}
	join := func(parts ...string) string {
		return " " + strings.Join(append([]string{"Ping Monitor"}, parts...), " · ") + " "
	}
	candidates := []string{
		join(append(shared, hiddenLabel(names)...)...),
		join("hidden: " + strings.Join(tr.hidden, " ")),
		join(fmt.Sprintf("%d columns hidden", len(tr.hidden))),
	}
	for _, title := range candidates {
		if runewidth.StringWidth(title) <= maxWidth {
			return title
		}
	}
	return plain
}

// hiddenLabel lists hidden column names, or nothing when there are none.
func hiddenLabel(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	return []string{"hidden: " + strings.Join(names, " ")}
}

// sharedValue reports the value every row has in column i, when there is one
// worth showing (not empty, "-" or the automatic source "Auto").
func sharedValue(texts [][]string, i int) (string, bool) {
	if len(texts) == 0 || i < 0 || i >= len(texts[0]) {
		return "", false
	}
	v := texts[0][i]
	if v == "" || v == "-" || v == "Auto" {
		return "", false
	}
	for _, row := range texts[1:] {
		if i >= len(row) || row[i] != v {
			return "", false
		}
	}
	return v, true
}
