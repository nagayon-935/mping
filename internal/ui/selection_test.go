package ui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestSelectionUsesVisibleDataRows(t *testing.T) {
	for _, tt := range []struct {
		name     string
		height   int
		visible  int
		pageStep int
	}{
		{"unsized", 0, 1, 10},
		{"tiny", 4, 1, 10},
		{"short", 12, 5, 10},
		{"minimum", 22, 10, 10},
		{"even", 26, 12, 12},
		{"odd", 27, 12, 12},
		{"tall even", 54, 26, 26},
		{"tall odd", 55, 26, 26},
	} {
		t.Run(tt.name, func(t *testing.T) {
			targets := make([]*stats.TargetStats, 100)
			for i := range targets {
				targets[i] = stats.NewTargetStats(fmt.Sprintf("host%03d", i))
			}
			tr := newTestTableRenderer(targets, 200)
			tr.tablePane.SetRect(0, 0, 200, tt.height+2) // Exclude the pane's outer borders.
			tr.table.SetRect(1, 1, 198, tt.height)
			tr.selectionEnabled = true
			tr.selectedID = targets[tt.visible-1].ID()
			tr.scrollToSelection = true
			tr.update()
			if offset, _ := tr.table.GetOffset(); offset != 0 {
				t.Errorf("last visible target scrolled to offset %d, want 0", offset)
			}

			tr.moveSelection(tcell.KeyDown)
			if offset, _ := tr.table.GetOffset(); offset != 1 {
				t.Errorf("first target below viewport scrolled to offset %d, want 1", offset)
			}

			tr.selectedID = targets[len(targets)-1].ID()
			tr.scrollToSelection = true
			tr.update()
			if offset, _ := tr.table.GetOffset(); offset != len(targets)-tt.visible {
				t.Errorf("bottom offset = %d, want %d", offset, len(targets)-tt.visible)
			}

			tr.selectedID = targets[0].ID()
			tr.moveSelection(tcell.KeyPgDn)
			if got := tr.selectedIndex(); got != tt.pageStep {
				t.Errorf("PgDn selected target %d, want %d", got, tt.pageStep)
			}
			tr.moveSelection(tcell.KeyPgUp)
			if got := tr.selectedIndex(); got != 0 {
				t.Errorf("PgUp selected target %d, want 0", got)
			}

			// Windowing keeps its minimum render budget even in a short pane.
			start, end := tr.visibleRowWindow(0, len(targets))
			if want := tt.pageStep + 1 + rowRenderMargin; start != 0 || end != want {
				t.Errorf("render window = [%d, %d), want [0, %d)", start, end, want)
			}
		})
	}
}
