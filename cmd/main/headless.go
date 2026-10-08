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

// newHeadlessRunner stands in for ui.Run under --no-tui. It prints the log
// lines the TUI would show in its Log pane and returns, like the TUI's 'q',
// when the iteration should end: a reload or --duration (ExternalCloseCh),
// --count completion (DoneCh) or SIGINT/SIGTERM. run()'s loop then reloads
// or exits exactly as it does after the TUI returns.
func newHeadlessRunner(out io.Writer, sigs <-chan os.Signal) func(ui.RunOptions) error {
	return func(opts ui.RunOptions) error {
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
			case s := <-sigs:
				note("Received %s, exiting", s)
				return nil
			}
		}
	}
}

// plainLogLine renders a Log-pane line without tview colour tags, using
// tview's own parser so escaped brackets (e.g. "[red[]") come out literally.
func plainLogLine(line string) string {
	text := tview.NewTextView().SetDynamicColors(true).SetText(line).GetText(true)
	return strings.TrimRight(text, "\n")
}
