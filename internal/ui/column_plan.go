package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

// textColumnSlack is how far a text column may
// grow past its widest value when the terminal has width to spare.
const textColumnSlack = 2

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

// planColumns fits the Ping Monitor's columns into availableTableWidth (the
// table's inner width, column separators included).
//
// Cells are never cut while a column can still be hidden instead: every shown
// column first gets at least its full content width, and columns are hidden
// in hideRank order until the rest fit. Only when every hideable column is
// hidden may text columns shrink below their content (an ellipsis); numeric
// columns reserve a fixed width. Columns marked
// hideWhenEmpty are left out whenever no row has a value for them.
func (tr *tableRenderer) planColumns(texts [][]string, availableTableWidth int) columnPlan {
	n := len(tr.cols)
	content := make([]int, n)
	desired := make([]int, n)
	maxW := make([]int, n)
	fullMin := make([]int, n)
	hardMin := make([]int, n)
	var active []int
	for i, c := range tr.cols {
		if c.fixed {
			active = append(active, i)
			desired[i], fullMin[i], hardMin[i], maxW[i] = c.base, c.base, c.base, c.base
			continue
		}
		content[i] = runewidth.StringWidth(c.name)
		empty := true
		for _, row := range texts {
			content[i] = max(content[i], runewidth.StringWidth(row[i]))
			if row[i] != "" && row[i] != "-" {
				empty = false
			}
		}
		if !empty {
			tr.everShown[i] = true
		}
		if c.hideWhenEmpty && !tr.everShown[i] {
			continue
		}
		active = append(active, i)
		text := c.dynamic || c.align == tview.AlignLeft
		if !c.dynamic {
			// Measured values change width from tick to tick; planning from
			// the widest seen keeps a column from flickering in and out.
			tr.widthSeen[i] = max(tr.widthSeen[i], content[i])
			content[i] = tr.widthSeen[i]
		}
		desired[i] = tr.baseWidths[i]
		if c.dynamic {
			desired[i] = content[i]
		}
		maxW[i] = max(tr.maxWidths[i], content[i])
		fullMin[i] = content[i]
		hardMin[i] = max(tr.minWidths[i], content[i])
		if text {
			// Text may be cut with an ellipsis as a last resort, down to
			// the width of its heading or the declared minimum.
			maxW[i] = content[i] + textColumnSlack
			hardMin[i] = min(maxW[i], max(tr.minWidths[i], runewidth.StringWidth(c.name)))
		}
		if !c.dynamic && c.align == tview.AlignLeft {
			// A free-text column (ERR) claims at most its usual maximum
			// before other columns are hidden for it.
			fullMin[i] = min(content[i], max(tr.maxWidths[i], runewidth.StringWidth(c.name)))
		}
	}

	hideOrder := slices.Clone(active)
	hideOrder = slices.DeleteFunc(hideOrder, func(i int) bool { return tr.cols[i].hideRank == 0 })
	slices.SortStableFunc(hideOrder, func(a, b int) int { return tr.cols[a].hideRank - tr.cols[b].hideRank })

	var hidden []string
	for next := 0; ; next++ {
		if widths, ok := tr.fitColumns(active, desired, fullMin, maxW, availableTableWidth); ok {
			return columnPlan{active: active, widths: widths, hidden: hidden, ok: true}
		}
		if next == len(hideOrder) {
			break
		}
		drop := hideOrder[next]
		active = slices.DeleteFunc(active, func(i int) bool { return i == drop })
		hidden = append(hidden, tr.cols[drop].name)
	}
	// Every hideable column is gone and text still has to be cut. Spare width
	// may remain (one long host name forced the cut), so bring back the
	// hidden columns that fit without cutting the shown text any further,
	// most important first.
	widths, ok := tr.fitColumns(active, desired, hardMin, maxW, availableTableWidth)
	if !ok {
		return columnPlan{active: active, widths: widths, hidden: hidden, ok: false}
	}
	for k := len(hidden) - 1; k >= 0; k-- {
		keep := slices.Clone(hardMin)
		for j, i := range active {
			keep[i] = min(widths[j], fullMin[i])
		}
		back := columnIndex(tr.cols, hidden[k])
		trial := append(slices.Clone(active), back)
		slices.Sort(trial)
		if w, fits := tr.fitColumns(trial, desired, keep, maxW, availableTableWidth); fits {
			active, widths = trial, w
			hidden = slices.Delete(hidden, k, k+1)
		}
	}
	return columnPlan{active: active, widths: widths, hidden: hidden, ok: true}
}

// fitColumns fits the active columns' widths to the table width left after
// the separators between them.
func (tr *tableRenderer) fitColumns(active, desired, minW, maxW []int, availableTableWidth int) ([]int, bool) {
	pick := func(all []int) []int {
		out := make([]int, len(active))
		for j, i := range active {
			out[j] = all[i]
		}
		return out
	}
	available := max(availableTableWidth-max(len(active)-1, 0), 0)
	return fitWidthsToAvailable(pick(desired), pick(minW), pick(maxW), pick(tr.shrinkPriorities), pick(tr.growPriorities), available)
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

// fitWidthsToAvailable clamps desired widths to [minWidths, maxWidths], then
// shrinks or grows them to sum to exactly availableColumnsWidth (when
// possible), visiting columns in ascending shrinkPriority/growPriority order
// — lower values are shrunk/grown first. Every priority slice must be the
// same length as desired; ties are broken by original index order (stable).
func fitWidthsToAvailable(desired, minWidths, maxWidths, shrinkPriority, growPriority []int, availableColumnsWidth int) ([]int, bool) {
	n := len(desired)
	if n != len(minWidths) || n != len(maxWidths) || n != len(shrinkPriority) || n != len(growPriority) {
		return nil, false
	}
	widths := make([]int, n)
	sumMin := 0
	for i := range desired {
		if minWidths[i] > maxWidths[i] {
			maxWidths[i] = minWidths[i]
		}
		w := max(desired[i], minWidths[i])
		if w > maxWidths[i] {
			w = maxWidths[i]
		}
		widths[i] = w
		sumMin += minWidths[i]
	}
	if availableColumnsWidth < sumMin {
		return nil, false
	}

	sum := 0
	for _, w := range widths {
		sum += w
	}

	shrinkOrder := indicesByPriority(shrinkPriority)
	for sum > availableColumnsWidth {
		changed := false
		for _, idx := range shrinkOrder {
			if widths[idx] > minWidths[idx] {
				widths[idx]--
				sum--
				changed = true
				if sum <= availableColumnsWidth {
					break
				}
			}
		}
		if !changed {
			break
		}
	}

	growOrder := indicesByPriority(growPriority)
	for sum < availableColumnsWidth {
		changed := false
		for _, idx := range growOrder {
			if widths[idx] < maxWidths[idx] {
				widths[idx]++
				sum++
				changed = true
				if sum >= availableColumnsWidth {
					break
				}
			}
		}
		if !changed {
			break
		}
	}

	return widths, true
}

// indicesByPriority returns 0..len(priority)-1 sorted by ascending priority
// value (stable, so equal priorities keep their original relative order).
func indicesByPriority(priority []int) []int {
	idx := make([]int, len(priority))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return priority[idx[a]] < priority[idx[b]] })
	return idx
}
