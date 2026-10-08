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

func TestReportSimulationSavesSessionAndSelectedDuplicate(t *testing.T) {
	previous := newApplication
	t.Cleanup(func() { newApplication = previous })
	frames := make(chan string, 256)
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
	type request struct {
		path, format string
		id           uint64
	}
	requests := make(chan request, 2)
	release := make(chan struct{})
	defer close(release)
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	done := make(chan error, 1)
	go func() {
		done <- Run(RunOptions{Targets: []*stats.TargetStats{a, b}, Interval: 50 * time.Millisecond, Timeout: time.Second, PacketSize: 56, OnSaveReport: func(path, format string, id uint64) error {
			requests <- request{path, format, id}
			if id != 0 {
				<-release
				return fmt.Errorf("test write denied")
			}
			return nil
		}})
	}()
	screen := <-screens
	frameHeight := 45
	wait := func(marker string) string {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case frame := <-frames:
				if strings.Contains(frame, marker) && strings.Count(frame, "\n") == frameHeight {
					return frame
				}
			case <-timer.C:
				t.Fatalf("frame missing %q", marker)
			}
		}
	}
	t.Cleanup(func() {
		screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
		screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	})
	wait("Ping Monitor")
	screen.InjectKey(tcell.KeyRune, 'w', tcell.ModNone)
	wait("Save session report")
	frame := wait("保存先 (Path) / 入力中")
	assertPathBox := func(frame string) {
		t.Helper()
		rows := strings.Split(frame, "\n")
		for y, row := range rows {
			if strings.Contains(row, "保存先 (Path) / 入力中") {
				if y+2 >= len(rows) || !strings.Contains(row, "╔") || !strings.Contains(row, "╗") ||
					!strings.Contains(rows[y+1], "mping-") || strings.Count(rows[y+1], "║") < 2 ||
					!strings.Contains(rows[y+2], "╚") || !strings.Contains(rows[y+2], "╝") {
					t.Fatalf("save path is not enclosed or is clipped:\n%s", frame)
				}
				return
			}
		}
		t.Fatalf("focused path missing:\n%s", frame)
	}
	assertPathBox(frame)
	// A smaller terminal must still show the field and its surrounding border.
	screen.SetSize(80, 24)
	frameHeight = 24
	screen.InjectKey(tcell.KeyCtrlL, 0, tcell.ModNone)
	assertPathBox(wait("保存先 (Path) / 入力中"))
	// Tab cycles through both actions and returns to the framed input.
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	wait(" 保存先 (Path) ─")
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	wait("保存先 (Path) / 入力中")
	screen.InjectKey(tcell.KeyCtrlU, 0, tcell.ModNone)
	for _, r := range "qfwz.json" {
		screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	wait("Format: JSON (.json)")
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	select {
	case r := <-requests:
		if r.path != "qfwz.json" || r.format != "json" || r.id != 0 {
			t.Fatalf("incorrect session save: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("save callback missing")
	}
	wait("Saved report: qfwz.json")
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	wait(fmt.Sprintf("Target #%d", b.ID))
	screen.InjectKey(tcell.KeyRune, 'w', tcell.ModNone)
	wait(fmt.Sprintf("Save target #%d report", b.ID))
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	select {
	case r := <-requests:
		if r.format != "text" || r.id != b.ID {
			t.Fatalf("incorrect target save: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("target callback missing")
	}
	// The background save is deliberately blocked: navigation must still work.
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	wait("Ping Monitor")
	release <- struct{}{}
	wait("Save report: test write denied")
	screen.InjectKey(tcell.KeyRune, 'w', tcell.ModNone)
	wait("Save session report")
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	wait("Ping Monitor")
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("UI did not exit")
	}
}
