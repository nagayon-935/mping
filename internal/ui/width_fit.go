package ui

import (
	"sort"
)

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
		w := desired[i]
		if w < minWidths[i] {
			w = minWidths[i]
		}
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
