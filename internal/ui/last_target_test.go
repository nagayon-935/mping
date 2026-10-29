package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestLastTargetDeletionOffersQuitAndCanReturn(t *testing.T) {
	const lastDialog = "最後のホストです。"
	for _, details := range []bool{false, true} {
		t.Run(fmt.Sprintf("details=%t", details), func(t *testing.T) {
			target := stats.NewTargetStats("last.example")
			deleted := make(chan uint64, 1)
			h := startTUI(t, RunOptions{
				Targets:        []*stats.TargetStats{target},
				OnDeleteTarget: func(id uint64) error { deleted <- id; return nil },
			}, 140, 45)
			// waitView waits for a frame showing marker with the last-host
			// dialog open or closed, as asked.
			waitView := func(marker string, dialog bool) {
				t.Helper()
				h.waitFrame(fmt.Sprintf("%q (dialog=%t)", marker, dialog), func(f string) bool {
					return strings.Contains(f, marker) && strings.Contains(f, lastDialog) == dialog
				})
			}
			waitView("Ping Monitor", false)
			marker := "Ping Monitor"
			if details {
				h.key(tcell.KeyEnter)
				marker = "Host Details"
				waitView(marker, false)
			}
			for _, cancel := range []tcell.Key{tcell.KeyEnter, tcell.KeyEscape} {
				h.press('d')
				waitView("終了するには q キーを押してください。", true)
				h.press('d')
				h.key(cancel)
				waitView(marker, false)
				select {
				case <-deleted:
					t.Fatal("last target was deleted")
				default:
				}
			}
			h.press('d')
			waitView("終了するには q キーを押してください。", true)
			h.quit()
			select {
			case <-deleted:
				t.Fatal("quitting deleted the last measurement")
			default:
			}
			if target.GetView().Host != "last.example" {
				t.Fatal("last measurement was lost")
			}
		})
	}
}
