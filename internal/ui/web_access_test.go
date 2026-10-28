package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestWebAccessSurvivesLogRotationAndCopiesFullURL(t *testing.T) {
	previous := newApplication
	t.Cleanup(func() { newApplication = previous })
	frames := make(chan string, 256)
	screens := make(chan tcell.SimulationScreen, 1)
	newApplication = func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		app.SetScreen(screen)
		screen.SetSize(80, 24)
		app.SetAfterDrawFunc(func(screen tcell.Screen) {
			var frame strings.Builder
			w, h := screen.Size()
			for y := range h {
				frame.WriteString(screenVisibleRow(screen, y, w))
				frame.WriteByte('\n')
			}
			select {
			case frames <- frame.String():
			default:
			}
		})
		screens <- screen
		return app
	}
	url := "http://127.0.0.1:8080/#token=" + strings.Repeat("t", 64)
	// No retained log line contains the URL: discovery cannot depend on Log.
	logs := make([]string, errorLogMaxSize+10)
	for i := range logs {
		logs[i] = "A newer event"
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(RunOptions{
			Targets:  []*stats.TargetStats{stats.NewTargetStats("example.com")},
			Interval: 50 * time.Millisecond, Timeout: time.Second,
			WebControlURL: url, InitialLogs: logs,
			OnAddHost: func(string) error { return nil },
			OnStop:    func() { t.Error("dialog forwarded a global stop shortcut") },
		})
	}()
	screen := <-screens
	wait := func(marker, absent string) string {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		last := ""
		for {
			select {
			case frame := <-frames:
				last = frame
				if strings.Contains(frame, marker) && (absent == "" || !strings.Contains(frame, absent)) {
					return frame
				}
			case <-timer.C:
				t.Fatalf("missing %q (excluding %q):\n%s", marker, absent, last)
			}
		}
	}
	t.Cleanup(func() {
		screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
		screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	})
	frame := wait("b: URL / Copy", "")
	if strings.Contains(frame, "#token=") {
		t.Fatal("permanent header must not expose the control token")
	}
	screen.InjectKey(tcell.KeyRune, 'b', tcell.ModNone)
	wait("Web UI access", "")
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	wait("Copy requested", "")
	if got := string(screen.GetClipboardData()); got != url {
		t.Fatalf("copied URL %q, want the full URL %q", got, url)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	wait("Ping Monitor", "Web UI access")
	// The same shortcut is available from details and restores that view.
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	wait("Host Details", "")
	screen.InjectKey(tcell.KeyRune, 'b', tcell.ModNone)
	wait("Web UI access", "")
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	wait("Host Details", "Web UI access")
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	wait("Ping Monitor", "Host Details")
	// Typing a host containing b must not open the URL dialog.
	screen.InjectKey(tcell.KeyRune, 'a', tcell.ModNone)
	screen.InjectKey(tcell.KeyRune, 'b', tcell.ModNone)
	wait("Add host: b", "Web UI access")
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("UI did not stop")
	}
}
