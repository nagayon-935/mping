package ui

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestDeleteConfirmationDoesNotReplaceDisappearingTarget(t *testing.T) {
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
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	type targetSet struct{ targets []*stats.TargetStats }
	var source atomic.Pointer[targetSet]
	source.Store(&targetSet{[]*stats.TargetStats{a, b}})
	refreshed := make(chan struct{})
	var once sync.Once
	deleted := make(chan uint64, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(RunOptions{Targets: []*stats.TargetStats{a, b}, Interval: 50 * time.Millisecond, Timeout: time.Second, PacketSize: 56,
			TargetSource: func() TargetSet {
				targets := source.Load().targets
				if len(targets) == 1 {
					once.Do(func() { close(refreshed) })
				}
				return TargetSet{Targets: targets}
			},
			OnDeleteTarget: func(id uint64) error { deleted <- id; return errors.New("selected target is no longer active") },
		})
	}()
	screen := <-screens
	t.Cleanup(func() {
		screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
		screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	})
	wait := func(marker string) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case frame := <-frames:
				if strings.Contains(frame, marker) {
					return
				}
			case <-timer.C:
				t.Fatalf("frame missing %q", marker)
			}
		}
	}
	wait("Ping Monitor")
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	wait("Host Details")
	screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
	wait("このホストを削除しますか？")
	source.Store(&targetSet{[]*stats.TargetStats{a}})
	b.RecordEvent("test", "removed elsewhere")
	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("membership did not refresh")
	}
	screen.InjectKey(tcell.KeyRight, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	select {
	case id := <-deleted:
		if id != b.ID {
			t.Fatal("confirmation substituted another target")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirmation callback missing")
	}
	wait("Delete target: selected target is no longer active")
	wait("Ping Monitor")
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("UI did not stop")
	}
}
