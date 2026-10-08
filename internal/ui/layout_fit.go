package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	// paneMinHeight keeps every unfolded pane at least a border plus one
	// content line tall, so a pane with nothing to show stays visible.
	paneMinHeight = 3
	// logLines is the content height the Log pane always keeps. The Log
	// never takes rows from other panes: it grows past this only into rows
	// no other pane needs (see paneControls.fit), and older messages are
	// read by scrolling or maximizing the pane.
	logLines = 5
	// graphPreferredRowHeight is the height of one RTT graph row (label
	// line, plot, separator) at which the plot uses its full grid: a plot of
	// 9 lines has 4 evenly spaced steps (see adjustPlotArea).
	graphPreferredRowHeight = 11
)

// paneDemand describes how much vertical space one pane wants.
type paneDemand struct {
	weight  int
	desired int // content height including borders
}

// fitHeights splits avail rows between panes. Panes whose content fits in
// their weighted share get exactly their content height; the rows they leave
// unused go to the panes that need more, again by weight. When every pane
// fits, spare is the number of rows nobody needs, for the caller to hand out
// so the layout never leaves blank space below short content.
func fitHeights(avail int, demands []paneDemand) (sizes []int, spare int) {
	sizes = make([]int, len(demands))
	if avail <= 0 {
		return sizes, 0
	}
	if len(demands) == 0 {
		return sizes, avail
	}
	open := make([]int, 0, len(demands))
	for i := range demands {
		open = append(open, i)
	}
	remaining := avail
	for {
		weightSum := 0
		for _, i := range open {
			weightSum += max(demands[i].weight, 1)
		}
		next := open[:0:0]
		for _, i := range open {
			share := remaining * max(demands[i].weight, 1) / weightSum
			if d := max(demands[i].desired, paneMinHeight); d <= share {
				sizes[i] = d
				remaining -= d
			} else {
				next = append(next, i)
			}
		}
		if len(next) == len(open) || len(next) == 0 {
			open = next
			break
		}
		open = next
	}
	if len(open) == 0 {
		return sizes, remaining
	}
	// Distribute the rest proportionally the same way tview's Flex does,
	// so rounding leftovers end up in the last open pane.
	weightSum := 0
	for _, i := range open {
		weightSum += max(demands[i].weight, 1)
	}
	for _, i := range open {
		w := max(demands[i].weight, 1)
		size := remaining * w / weightSum
		sizes[i] = size
		remaining -= size
		weightSum -= w
	}
	return sizes, 0
}

// textLineCount returns the number of display lines in unwrapped text,
// ignoring a single trailing newline.
func textLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// fitFlex is the main layout: before each draw it asks fit to size the
// panes for the current height.
type fitFlex struct {
	*tview.Flex
	fit func(width, height int)
}

func (f *fitFlex) Draw(screen tcell.Screen) {
	_, _, width, height := f.GetInnerRect()
	f.fit(width, height)
	f.Flex.Draw(screen)
}
