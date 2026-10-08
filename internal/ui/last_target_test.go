package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestLastTargetDeletionOffersQuitAndCanReturn(t *testing.T) {
	for _, details := range []bool{false, true} {
		t.Run(fmt.Sprintf("details=%t", details), func(t *testing.T) {
			previous := newApplication
			t.Cleanup(func() { newApplication = previous })
			frames := make(chan string, 128)
			screens := make(chan tcell.SimulationScreen, 1)
			newApplication = func() *tview.Application {
				app := tview.NewApplication()
				screen := tcell.NewSimulationScreen("UTF-8")
				app.SetScreen(screen)
				screen.SetSize(140, 45)
				app.SetAfterDrawFunc(func(screen tcell.Screen) {
					var b strings.Builder
					w, h := screen.Size()
					for y := 0; y < h; y++ {
						b.WriteString(screenVisibleRow(screen, y, w))
						b.WriteByte('\n')
					}
					select {
					case frames <- b.String():
					default:
					}
				})
				screens <- screen
				return app
			}
			target := stats.NewTargetStats("last.example")
			deleted := make(chan uint64, 1)
			done := make(chan error, 1)
			go func() {
				done <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: time.Second, PacketSize: 56,
					OnDeleteTarget: func(id uint64) error { deleted <- id; return nil },
				})
			}()
			screen := <-screens
			t.Cleanup(func() {
				screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
				screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
			})
			wait := func(marker string, requireDialog bool) {
				t.Helper()
				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				for {
					select {
					case frame := <-frames:
						if strings.Contains(frame, marker) && strings.Contains(frame, "最後のホストです。") == requireDialog {
							return
						}
					case <-timer.C:
						t.Fatalf("frame missing %q (dialog=%t)", marker, requireDialog)
					}
				}
			}
			wait("Ping Monitor", false)
			marker := "Ping Monitor"
			if details {
				screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
				marker = "Host Details"
				wait(marker, false)
			}
			for _, cancel := range []tcell.Key{tcell.KeyEnter, tcell.KeyEscape} {
				screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
				wait("終了するには q キーを押してください。", true)
				screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
				screen.InjectKey(cancel, 0, tcell.ModNone)
				wait(marker, false)
				select {
				case <-deleted:
					t.Fatal("last target was deleted")
				default:
				}
			}
			screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
			wait("終了するには q キーを押してください。", true)
			screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("q did not terminate the UI")
			}
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
