package ui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestCountCompletionKeepsExternalCloseActive(t *testing.T) {
	original := newApplication
	t.Cleanup(func() { newApplication = original })
	completedDraw := make(chan struct{})
	var once sync.Once
	newApplication = func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		app.SetScreen(screen)
		screen.SetSize(200, 50)
		app.SetAfterDrawFunc(func(screen tcell.Screen) {
			_, height := screen.Size()
			var line []rune
			for x := 0; x < 100; x++ {
				r, _, _, _ := screen.GetContent(x, height-1)
				line = append(line, r)
			}
			// Read only on the draw goroutine; no concurrent screen inspection.
			if strings.Contains(string(line), "Finished.") {
				once.Do(func() { close(completedDraw) })
			}
		})
		return app
	}
	doneCh := make(chan struct{}, 1)
	doneCh <- struct{}{}
	externalClose := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{stats.NewTargetStats("localhost")}, Interval: time.Second, DoneCh: doneCh, ExternalCloseCh: externalClose})
	}()
	select {
	case <-completedDraw:
	case <-time.After(3 * time.Second):
		t.Fatal("completion footer was not drawn")
	}
	close(externalClose)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("count completion disabled duration/reload handling")
	}
}
