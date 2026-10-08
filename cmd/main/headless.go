package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
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

// headlessRunner stands in for ui.Run under --no-tui. Each run prints the
// log lines the TUI would show in its Log pane and returns, like the TUI's
// 'q', when the iteration should end: a reload or --duration
// (ExternalCloseCh), --count completion (DoneCh) or SIGINT/SIGTERM.
//
// A signal is a request to exit, not just to end the iteration: run()
// checks quitRequested before honouring any reload that became pending
// meanwhile. The first signal also hands signal handling back to the OS, so
// a second Ctrl-C terminates a shutdown that has stalled.
type headlessRunner struct {
	out         io.Writer
	sigs        <-chan os.Signal
	stopSignals func()
	quit        atomic.Bool
}

func newHeadlessRunner(out io.Writer, sigs <-chan os.Signal, stopSignals func()) *headlessRunner {
	return &headlessRunner{out: out, sigs: sigs, stopSignals: stopSignals}
}

func (h *headlessRunner) quitRequested() bool { return h.quit.Load() }

func (h *headlessRunner) run(opts ui.RunOptions) error {
	for _, line := range opts.InitialLogs {
		h.logLine(line)
	}
	for {
		select {
		case line := <-opts.ExternalLogCh:
			h.logLine(line)
		case <-opts.ExternalCloseCh:
			h.drain(opts.ExternalLogCh)
			h.note("Reloading configuration...")
			return nil
		case <-opts.DoneCh:
			h.drain(opts.ExternalLogCh)
			h.note("Finished: --count reached for every target")
			return nil
		case s := <-h.sigs:
			h.quit.Store(true)
			h.stopSignals()
			h.drain(opts.ExternalLogCh)
			h.note("Received %s, exiting (press Ctrl-C again to force)", s)
			return nil
		}
	}
}

// drain prints log lines already queued when the iteration ends, so the
// last route flap or web edit before exiting is not lost.
func (h *headlessRunner) drain(logCh <-chan string) {
	for {
		select {
		case line := <-logCh:
			h.logLine(line)
		default:
			return
		}
	}
}

func (h *headlessRunner) logLine(line string) { fmt.Fprintln(h.out, plainLogLine(line)) }

func (h *headlessRunner) note(format string, args ...any) {
	fmt.Fprintf(h.out, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// plainLogLine renders a Log-pane line without tview colour tags, using
// tview's own parser so escaped brackets (e.g. "[red[]") come out literally.
func plainLogLine(line string) string {
	text := tview.NewTextView().SetDynamicColors(true).SetText(line).GetText(true)
	return strings.TrimRight(text, "\n")
}
