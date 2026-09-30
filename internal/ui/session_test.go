package ui

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func awaitSessionSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("session operation did not finish")
	}
}

func TestSessionStopReleasesFullMailbox(t *testing.T) {
	s := newUISession()
	defer s.Wait()
	for range sessionQueueSize {
		s.Post(func() { t.Error("update ran after stop") })
	}
	finished := make(chan struct{})
	go func() { s.Post(func() {}); close(finished) }()
	s.Stop()
	awaitSessionSignal(t, finished)
	if s.Post(func() {}) || s.Submit(func() {}) {
		t.Fatal("stopped session accepted work")
	}
}

func TestSessionStopJoinsRunningCallbackAndDiscardsQueuedWork(t *testing.T) {
	s := newUISession()
	entered, release := make(chan struct{}), make(chan struct{})
	s.Submit(func() { close(entered); <-release })
	awaitSessionSignal(t, entered)
	s.Submit(func() { t.Error("queued callback ran after stop") })
	s.Stop()
	joined := make(chan struct{})
	go func() { s.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Error("Wait returned while callback was running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	awaitSessionSignal(t, joined)
}

func TestInputOperationsKeepStopRestartResetOrder(t *testing.T) {
	s := newUISession()
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer func() { releaseOnce.Do(func() { close(release) }); s.Stop(); s.Wait() }()
	app := tview.NewApplication()
	table := tview.NewTable()
	app.SetFocus(table)
	target := stats.NewTargetStats("example.com")
	target.IncSent()
	vs := newViewState(tview.NewTextView())
	entered, reset := make(chan struct{}), make(chan struct{})
	var operations []string // only read after reset closes
	rows := 1
	handler := newInputHandler(inputHandlerDeps{
		app: app, table: table, addHostInput: tview.NewInputField(), deleteHostInput: tview.NewInputField(),
		vs: vs, rowCount: &rows, targets: []*stats.TargetStats{target}, session: s,
		onStop:    func() { close(entered); <-release; operations = append(operations, "stop") },
		onRestart: func() error { operations = append(operations, "restart"); return nil },
		onReset:   func() { operations = append(operations, "reset"); target.Reset(); close(reset) },
	})
	handler(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
	awaitSessionSignal(t, entered)
	for _, key := range "SSR" { // repeated S must not enqueue a second restart
		handler(tcell.NewEventKey(tcell.KeyRune, key, tcell.ModNone))
	}
	if target.GetView().Sent != 1 {
		t.Fatal("reset overtook the blocked stop")
	}
	releaseOnce.Do(func() { close(release) })
	awaitSessionSignal(t, reset)
	if !reflect.DeepEqual(operations, []string{"stop", "restart", "reset"}) || target.GetView().Sent != 0 {
		t.Fatalf("operations=%v, sent=%d", operations, target.GetView().Sent)
	}
}

func TestSessionPrivateWakeDeliversOnlyOnInputLoop(t *testing.T) {
	s := newUISession()
	defer func() { s.Stop(); s.Wait() }()
	app := tview.NewApplication()
	inputs, updates := 0, 0
	s.bind(app, func(e *tcell.EventKey) *tcell.EventKey { inputs++; return e })
	s.Post(func() { updates++ })
	if updates != 0 {
		t.Fatal("producer executed a UI update")
	}
	capture := app.GetInputCapture()
	capture(s.wakeKey)
	capture(tcell.NewEventKey(tcell.KeyRune, 0, tcell.ModNone))
	if updates != 1 || inputs != 1 {
		t.Fatalf("updates=%d, inputs=%d", updates, inputs)
	}
}

func TestRunJoinsHostCallbackErrorAfterAppStop(t *testing.T) {
	for _, key := range []rune{'a', 'd'} {
		t.Run(string(key), func(t *testing.T) {
			orig := newApplication
			t.Cleanup(func() { newApplication = orig })
			ready, entered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var app *tview.Application
			var screen tcell.SimulationScreen
			newApplication = func() *tview.Application {
				app, screen = newSimApp(t)
				app.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
				return app
			}
			callback := func(string) error { close(entered); <-release; return errors.New("late error") }
			finished := make(chan error, 1)
			go func() {
				finished <- Run(RunOptions{
					Targets: []*stats.TargetStats{stats.NewTargetStats("example.com")}, Interval: time.Second,
					OnAddHost: callback, OnDeleteHost: callback,
				})
			}()
			awaitSessionSignal(t, ready)
			screen.InjectKey(tcell.KeyRune, key, tcell.ModNone)
			for _, r := range "1.2.3.4" {
				screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			awaitSessionSignal(t, entered)
			app.Stop()
			select {
			case <-finished:
				t.Error("Run returned before joining its callback")
			default:
			}
			close(release)
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("late callback error blocked after app.Stop")
			}
		})
	}
}
