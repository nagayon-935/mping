package ui

import (
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestRunWithSimulationScreen(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	newApplication = func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		app.SetScreen(screen)
		screen.SetSize(80, 24)
		go func() {
			time.Sleep(30 * time.Millisecond)
			app.Stop()
		}()
		return app
	}

	target := stats.NewTargetStats("example.com")
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(done)
	}()

	err := Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, DoneCh: done, PacketSize: 56})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// ---- Run() with traceEnabled, portEnabled, both, initialLogs, doneCh, keyboard ----

func makeSimScreen(t *testing.T) (func() *tview.Application, chan tcell.SimulationScreen) {
	t.Helper()
	ch := make(chan tcell.SimulationScreen, 1)
	return func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		screen.Init()
		app.SetScreen(screen)
		screen.SetSize(200, 50)
		ch <- screen
		return app
	}, ch
}

func TestRunWithTraceEnabled(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56, TraceEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
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

func TestRunWithPortEnabled(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{
		{Port: 443, Protocol: "tcp", Status: "Open"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
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

func TestRunWithBothTracAndPort(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{
		{Port: 80, Protocol: "tcp", Status: "Closed"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, TraceEnabled: true, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
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

func TestRunWithInitialLogs(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	logs := []string{"[red]initial error 1[-]", "[red]initial error 2[-]"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, InitialLogs: logs})
	}()

	screen := <-screenCh
	time.Sleep(30 * time.Millisecond)
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

func TestRunWithDoneChClosed(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	doneCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, DoneCh: doneCh, PacketSize: 56})
	}()

	screen := <-screenCh
	// Close doneCh to trigger "Finished" footer path
	time.Sleep(15 * time.Millisecond)
	close(doneCh)
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

// ---- Run: target with loss → error log entry ----

func TestRunWithLossTarget(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")
	target.IncSent()
	target.OnFailure("Timeout")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(80 * time.Millisecond) // wait for at least one tick to update table
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

// ---- Run: narrow screen triggers compact layout path ----

func makeNarrowSimScreen(t *testing.T) (func() *tview.Application, chan tcell.SimulationScreen) {
	t.Helper()
	ch := make(chan tcell.SimulationScreen, 1)
	return func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		screen.Init()
		app.SetScreen(screen)
		// Width 70: too narrow for full 13-col layout (~89 min) but fits compact (~55 min)
		screen.SetSize(70, 30)
		ch <- screen
		return app
	}, ch
}

func TestRunNarrowScreenCompactLayout(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeNarrowSimScreen(t)
	newApplication = factory

	targets := make([]*stats.TargetStats, 3)
	for i := range targets {
		targets[i] = stats.NewTargetStats("host")
		targets[i].IncSent()
		targets[i].OnSuccess(10*time.Millisecond, 64)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: targets, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	// Wait for multiple ticks so updateTable runs and compact layout is evaluated
	time.Sleep(150 * time.Millisecond)
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

// ---- Run: portEnabled with no port results (rowCount==0 path) ----

func TestRunPortEnabledNoResults(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	// No PortResults → triggers "Waiting for results..." row

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(120 * time.Millisecond)
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

// ---- Run: high RTT and jitter triggers alert state ----

func TestRunWithHighRTTAndJitter(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")
	// Simulate high RTT (> 200ms red threshold) and high jitter (> 50ms red threshold)
	for i := 0; i < 5; i++ {
		target.IncSent()
		target.OnSuccess(300*time.Millisecond, 64)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	// Wait > 100ms for ticker to fire so updateTable runs and alerts are checked
	time.Sleep(150 * time.Millisecond)
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

// ---- Run: high loss rate triggers lossRed alert ----

func TestRunWithHighLossAlert(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("1.1.1.1")
	// Simulate >80% loss: 9 packets lost, 1 success
	for i := 0; i < 9; i++ {
		target.IncSent()
		target.OnFailure("Timeout")
	}
	target.IncSent()
	target.OnSuccess(10*time.Millisecond, 64)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
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

// ---- Run: traceEnabled with ticker (>100ms wait) to cover updateTable trace path ----

func TestRunWithTraceAndTicker(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetTraceHops([]string{"10.0.0.1", "10.0.0.2", "1.1.1.1"})

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", PacketSize: 56, TraceEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond) // wait for ticker to fire and updateTable to run
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

// ---- Run: portEnabled with ticker (>100ms wait) to cover updateTable port path ----

func TestRunWithPortAndTicker(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.PortResults = []*stats.PortCheckResult{
		{Port: 443, Protocol: "tcp", Status: "Open"},
		{Port: 80, Protocol: "tcp", Status: "Closed"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
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

// ---- Run: both trace and port with ticker ----

func TestRunWithBothAndTicker(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	target.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})
	target.PortResults = []*stats.PortCheckResult{
		{Port: 443, Protocol: "tcp", Status: "Open"},
	}

	// Two targets so route separator row fires
	target2 := stats.NewTargetStats("example2.com")
	target2.SetIP("8.8.8.8")
	target2.SetTraceHops([]string{"10.0.0.1"})
	target2.PortResults = []*stats.PortCheckResult{
		{Port: 80, Protocol: "tcp", Status: "Filtered"},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target, target2}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, SourceIPv4: "10.0.0.1", SourceIPv6: "2001::1", PacketSize: 56, TraceEnabled: true, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
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

// ---- Run: target with IP==host covers calcColumnWidths value=view.IP branch ----

func TestRunWithIPAsHost(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	// Host and IP are the same → value = view.IP branch in calcColumnWidths
	target := stats.NewTargetStats("1.2.3.4")
	target.SetIP("1.2.3.4")

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond) // wait for ticker
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

// ---- Run: port status change logs message to error pane ----

func TestRunPortStatusChangeLogged(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	result := &stats.PortCheckResult{Port: 443, Protocol: "tcp"}
	result.SetResult("Open", 5*time.Millisecond)
	target.PortResults = []*stats.PortCheckResult{result}

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	// Let one render cycle run so the initial status is recorded
	time.Sleep(120 * time.Millisecond)

	// Change port status to trigger change detection on next render
	result.SetResult("Closed", 0)
	time.Sleep(120 * time.Millisecond)

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

// ---- Run: port with non-zero LastChange covers changeStr=formatLossAgo path ----

func TestRunWithPortLastChange(t *testing.T) {
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	factory, screenCh := makeSimScreen(t)
	newApplication = factory

	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	result := &stats.PortCheckResult{Port: 443, Protocol: "tcp", Status: "Checking..."}
	target.PortResults = []*stats.PortCheckResult{result}
	// Trigger LastChange by changing status
	result.SetResult("Open", 10*time.Millisecond)
	result.SetResult("Closed", 5*time.Millisecond) // status change sets LastChange

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(RunOptions{Targets: []*stats.TargetStats{target}, Interval: 50 * time.Millisecond, Timeout: 50 * time.Millisecond, PacketSize: 56, PortEnabled: true})
	}()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond)
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
