package main

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
	"github.com/rivo/tview"
)

// lockedBuffer is a bytes.Buffer safe for the runner goroutine and the test.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func runHeadlessAsync(opts ui.RunOptions, sigs <-chan os.Signal) (*lockedBuffer, <-chan error) {
	out := &lockedBuffer{}
	errc := make(chan error, 1)
	go func() { errc <- newHeadlessRunner(out, sigs)(opts) }()
	return out, errc
}

func waitReturn(t *testing.T, errc <-chan error) {
	t.Helper()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("runner error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("headless runner did not return")
	}
}

func TestHeadlessRunnerPrintsLogsAsPlainText(t *testing.T) {
	closeCh := make(chan struct{})
	close(closeCh)
	opts := ui.RunOptions{
		InitialLogs:     []string{"[yellow][15:04:05] port: change detected[-]", "Web UI: " + tview.Escape("http://h/[x]")},
		ExternalCloseCh: closeCh,
	}

	out, errc := runHeadlessAsync(opts, nil)
	waitReturn(t, errc)

	got := out.String()
	for _, want := range []string{"[15:04:05] port: change detected\n", "Web UI: http://h/[x]\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "[yellow]") || strings.Contains(got, "[-]") {
		t.Errorf("output %q still has tview colour tags", got)
	}
}

func TestHeadlessRunnerStreamsExternalLogLines(t *testing.T) {
	logCh := make(chan string, 1)
	closeCh := make(chan struct{})
	opts := ui.RunOptions{ExternalLogCh: logCh, ExternalCloseCh: closeCh}
	out, errc := runHeadlessAsync(opts, nil)

	logCh <- "[red]route flap on a.example[-]"
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(out.String(), "route flap on a.example") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	close(closeCh)
	waitReturn(t, errc)

	if !strings.Contains(out.String(), "route flap on a.example\n") {
		t.Fatalf("output %q lacks the streamed log line", out.String())
	}
}

func TestHeadlessRunnerReturnsWhen(t *testing.T) {
	tests := []struct {
		name    string
		trigger func(closeCh, doneCh chan struct{}, sigs chan os.Signal)
		want    string
	}{
		{"a reload or --duration closes the iteration", func(c, _ chan struct{}, _ chan os.Signal) { close(c) }, "Reloading"},
		{"--count completes", func(_, d chan struct{}, _ chan os.Signal) { d <- struct{}{} }, "Finished"},
		{"an interrupt arrives", func(_, _ chan struct{}, s chan os.Signal) { s <- os.Interrupt }, "Received interrupt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closeCh, doneCh := make(chan struct{}), make(chan struct{}, 1)
			sigs := make(chan os.Signal, 1)
			out, errc := runHeadlessAsync(ui.RunOptions{ExternalCloseCh: closeCh, DoneCh: doneCh}, sigs)

			tt.trigger(closeCh, doneCh, sigs)
			waitReturn(t, errc)

			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("output %q lacks %q", out.String(), tt.want)
			}
		})
	}
}

func TestParseArgsNoTUIDefaultsOff(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"example.com"})
	if err != nil {
		t.Fatal(err)
	}
	on, _, _, _, err := parseArgs([]string{"--no-tui", "example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.noTUI || !on.noTUI {
		t.Fatalf("noTUI default=%v with flag=%v, want false/true", cfg.noTUI, on.noTUI)
	}
}

// stubHeadlessSignals feeds run()'s headless loop from a test channel.
func stubHeadlessSignals(t *testing.T) chan os.Signal {
	t.Helper()
	orig := headlessSignals
	t.Cleanup(func() { headlessSignals = orig })
	ch := make(chan os.Signal, 1)
	headlessSignals = func() (<-chan os.Signal, func()) { return ch, func() {} }
	return ch
}

func TestRunNoTUIRunsWithoutTheTUIUntilInterrupted(t *testing.T) {
	stubRunSeams(t, func(ui.RunOptions) error {
		t.Error("the TUI was started under --no-tui")
		return nil
	})
	sigs := stubHeadlessSignals(t)
	go func() {
		time.Sleep(100 * time.Millisecond)
		sigs <- os.Interrupt
	}()

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--no-tui", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	for _, want := range []string{"Received interrupt", "--- mping statistics ---", "example.com"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout %q lacks %q", out.String(), want)
		}
	}
}

func TestRunNoTUIHidesTheControlTokenFromNonTerminalOutput(t *testing.T) {
	started := stubRunSeams(t, nil)
	sigs := stubHeadlessSignals(t)
	go func() {
		time.Sleep(100 * time.Millisecond)
		sigs <- os.Interrupt
	}()

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--no-tui", "--web", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Web UI: "+started().URL()) {
		t.Errorf("stdout %q does not announce the read-only URL", out.String())
	}
	if strings.Contains(out.String(), "#token=") {
		t.Errorf("stdout %q leaks the control token into non-terminal output", out.String())
	}
	if !strings.Contains(out.String(), "MPING_WEB_TOKEN") {
		t.Errorf("stdout %q does not explain how to enable control", out.String())
	}
}

func TestRunUsesMPINGWEBTOKENForTheWebServer(t *testing.T) {
	token := strings.Repeat("t", 32)
	t.Setenv("MPING_WEB_TOKEN", token)
	stubRunSeams(t, func(ui.RunOptions) error { return nil })
	var got string
	webStart = func(opts web.Options) (*web.Server, error) {
		got = opts.Token
		opts.Port = 0
		return web.Start(opts)
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("run = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if got != token {
		t.Fatalf("web token = %q, want MPING_WEB_TOKEN", got)
	}
}

func TestRunRejectsAWeakMPINGWEBTOKENWithoutEchoingIt(t *testing.T) {
	t.Setenv("MPING_WEB_TOKEN", "short")
	stubRunSeams(t, func(ui.RunOptions) error {
		t.Error("UI started with an invalid token")
		return nil
	})

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "--web", "example.com"}, &out, &errOut)

	if code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "MPING_WEB_TOKEN") || strings.Contains(errOut.String(), "short") {
		t.Fatalf("stderr = %q, want an MPING_WEB_TOKEN error that does not echo the value", errOut.String())
	}
}
