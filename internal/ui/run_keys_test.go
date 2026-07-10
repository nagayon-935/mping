package ui

import (
	"errors"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestRunKeyboardStop(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	stopCalled := make(chan struct{}, 1)
	onStop := func() { stopCalled <- struct{}{} }

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, OnStop: onStop})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Press 's' to stop
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	time.Sleep(30 * time.Millisecond)
	// Press 'q' to quit
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunKeyboardReset(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.IncSent()
	target.OnSuccess(10*time.Millisecond, 64)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Press 'R' to reset stats
	screen.InjectKey(tcell.KeyRune, 'R', tcell.ModNone)
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunKeyboardRestart(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	restartCalled := make(chan struct{}, 1)
	onStop := func() {}
	onRestart := func() error {
		restartCalled <- struct{}{}
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, OnStop: onStop, OnRestart: onRestart})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone) // stop first
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'S', tcell.ModNone) // restart
	time.Sleep(50 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunKeyboardTab(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab cycles focus between table, graph, errorView
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run with Tab when trace + port enabled (covers more Tab branches) ----

func TestRunTabWithTraceAndPort(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{{Port: 443, Protocol: "tcp", Status: "Open"}}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab multiple times to cycle through: table→trace→port→graph→error→table
	for i := 0; i < 6; i++ {
		screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: 's' key when onStop is nil (stopRequested path without callback) ----

func TestRunKeyboardStopNoCallback(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")

	errCh := make(chan error, 1)
	go func() {
		// onStop=nil, onRestart=nil
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone) // stop (no callback)
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone) // second 's' is no-op (already stopped)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: table key scroll (KeyDown when table is focused) ----

func TestRunTableScrollKeys(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	// Many targets so there are rows to scroll
	targets := make([]*stats.TargetStats, 12)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: targets, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Table should be focused initially; send scroll keys
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyUp, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgUp, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: onRestart returning error ----

func TestRunRestartError(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	onStop := func() {}
	restartErr := errors.New("restart failed")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, OnStop: onStop, OnRestart: func() error { return restartErr }})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	time.Sleep(20 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'S', tcell.ModNone)
	time.Sleep(60 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: Tab with traceEnabled only (covers traceView→graphView branch) ----

func TestRunTabTraceOnly(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab cycles: table→traceView→graphView→errorView→table
	for i := 0; i < 5; i++ {
		screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: Tab with portEnabled only (covers table→portView branch) ----

func TestRunTabPortOnly(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{{Port: 443, Protocol: "tcp", Status: "Open"}}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Tab cycles: table→portView→graphView→errorView→table
	for i := 0; i < 5; i++ {
		screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: scroll key with 1 target (maxOffset < 0 path) ----

func TestRunScrollKeyWithFewTargets(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	// Only 1 target → rowCount=2 < tableMaxRows+1=11 → maxOffset < 0 → clamped to 0
	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(20 * time.Millisecond)
	// Press scroll keys while table is focused (few targets → maxOffset < 0 path)
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyUp, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyPgUp, 0, tcell.ModNone)
	time.Sleep(5 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: 'R' key with traceEnabled clears TraceHops and calls onResetTrace ----

func TestRunResetTraceKey(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})

	resetCalled := make(chan struct{}, 1)
	onResetTrace := func() { resetCalled <- struct{}{} }

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true, OnResetTrace: onResetTrace})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'R', tcell.ModNone)

	// onResetTrace must be called.
	select {
	case <-resetCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("onResetTrace was not called after 'R' key")
	}

	// TraceHops must have been cleared.
	if hops := target.GetView().TraceHops; len(hops) != 0 {
		t.Errorf("TraceHops not cleared after 'R': got %v", hops)
	}

	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// ---- Run: 'R' key with portEnabled calls onResetPort ----

func TestRunResetPortKey(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	result := &stats.PortCheckResult{Port: 80, Protocol: "tcp"}
	result.SetResult("Open", 5*time.Millisecond)
	target.PortResults = []*stats.PortCheckResult{result}

	resetCalled := make(chan struct{}, 1)
	onResetPort := func() { resetCalled <- struct{}{} }

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true, OnResetPort: onResetPort})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
	screen.InjectKey(tcell.KeyRune, 'R', tcell.ModNone)

	select {
	case <-resetCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("onResetPort was not called after 'R' key")
	}

	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func newSimApp(t *testing.T) (*tview.Application, tcell.SimulationScreen) {
	t.Helper()
	app := tview.NewApplication()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	screen.SetSize(120, 40)
	app.SetScreen(screen)
	return app, screen
}

func TestRunOnAddHost_CallbackInvoked(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	addCh := make(chan string, 1)

	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			// Simulate 'a' key to open add dialog
			screen.InjectKey(tcell.KeyRune, 'a', tcell.ModNone)
			time.Sleep(20 * time.Millisecond)
			// Type hostname (use IP to avoid triggering key shortcuts)
			for _, r := range "1.2.3.4" {
				screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			time.Sleep(20 * time.Millisecond)
			// Press Enter to confirm
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			time.Sleep(100 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("example.com")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnAddHost: func(host string) error {
			addCh <- host
			return errors.New("test stop") // return error so TUI doesn't reload
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var got string
	select {
	case got = <-addCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("OnAddHost was not called within timeout")
	}
	if got != "1.2.3.4" {
		t.Fatalf("OnAddHost called with %q, want %q", got, "1.2.3.4")
	}
}

func TestRunOnAddHost_EmptyHostIgnored(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	addCalled := false
	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			screen.InjectKey(tcell.KeyRune, 'a', tcell.ModNone)
			time.Sleep(10 * time.Millisecond)
			// Press Enter without typing anything
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			time.Sleep(30 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("example.com")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnAddHost: func(host string) error {
			addCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if addCalled {
		t.Fatal("OnAddHost should not be called for empty host")
	}
}

func TestRunOnDeleteHost_CallbackInvoked(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	deleteCh := make(chan string, 1)

	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			// Simulate 'd' key to open delete dialog
			screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
			time.Sleep(20 * time.Millisecond)
			// Type the hostname to delete
			for _, r := range "8.8.8.8" {
				screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			time.Sleep(20 * time.Millisecond)
			// Press Enter to confirm
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			time.Sleep(100 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("8.8.8.8")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnDeleteHost: func(host string) error {
			deleteCh <- host
			return errors.New("test stop")
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var got string
	select {
	case got = <-deleteCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("OnDeleteHost was not called within timeout")
	}
	if got != "8.8.8.8" {
		t.Fatalf("OnDeleteHost called with %q, want %q", got, "8.8.8.8")
	}
}

func TestRunOnDeleteHost_EscapeAborts(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	deleteCalled := false
	newApplication = func() *tview.Application {
		app, screen := newSimApp(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			screen.InjectKey(tcell.KeyRune, 'd', tcell.ModNone)
			time.Sleep(20 * time.Millisecond)
			// Type something then press Escape to cancel
			for _, r := range "8.8.8.8" {
				screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			time.Sleep(10 * time.Millisecond)
			screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
			time.Sleep(50 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("8.8.8.8")
	err := Run(RunOptions{
		Targets:  []*stats.TargetStats{target},
		Interval: 50 * time.Millisecond,
		Timeout:  50 * time.Millisecond,
		OnDeleteHost: func(host string) error {
			deleteCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if deleteCalled {
		t.Fatal("OnDeleteHost should not be called after Escape")
	}
}
