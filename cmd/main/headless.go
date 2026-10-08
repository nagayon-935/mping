package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nagayon-935/mping/internal/ui"
	"github.com/rivo/tview"
	"golang.org/x/term"
)

// headlessSignals subscribes to the signals that end a --no-tui run. A seam
// for tests; the returned func unsubscribes.
var headlessSignals = func() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch, func() { signal.Stop(ch) }
}

// isTerminal reports whether w is an interactive terminal (not a pipe,
// file or journal), where it is safe to print the web control token.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// headlessRunner stands in for ui.Run under --no-tui (old behaviour, API only).
type headlessRunner struct {
	out  io.Writer
	sigs <-chan os.Signal
}

func newHeadlessRunner(out io.Writer, sigs <-chan os.Signal, stopSignals func()) *headlessRunner {
	return &headlessRunner{out: out, sigs: sigs}
}

func (h *headlessRunner) quitRequested() bool { return false }

func (h *headlessRunner) run(opts ui.RunOptions) error {
	out := h.out
	logLine := func(line string) { fmt.Fprintln(out, plainLogLine(line)) }
	note := func(format string, args ...any) {
		fmt.Fprintf(out, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}
	for _, line := range opts.InitialLogs {
		logLine(line)
	}
	for {
		select {
		case line := <-opts.ExternalLogCh:
			logLine(line)
		case <-opts.ExternalCloseCh:
			note("Reloading configuration...")
			return nil
		case <-opts.DoneCh:
			note("Finished: --count reached for every target")
			return nil
		case s := <-h.sigs:
			note("Received %s, exiting", s)
			return nil
		}
	}
}

// plainLogLine renders a Log-pane line without tview colour tags, using
// tview's own parser so escaped brackets (e.g. "[red[]") come out literally.
func plainLogLine(line string) string {
	text := tview.NewTextView().SetDynamicColors(true).SetText(line).GetText(true)
	return strings.TrimRight(text, "\n")
}
