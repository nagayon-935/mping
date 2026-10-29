package ui

import (
	"errors"
	"testing"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
)

// TestRunHostPrompts drives the add-host (a) and delete-host (d) prompts.
// Both return an error from their callback so the TUI does not reload, and
// the error must reach the Log pane.
//
// The abandon cases open a second prompt and submit a sentinel host. Callbacks
// run FIFO on one worker, so if the abandoned prompt had submitted anything
// it would arrive before the sentinel.
func TestRunHostPrompts(t *testing.T) {
	kinds := []struct {
		name   string
		key    rune
		prompt string
		wire   func(opts *RunOptions, callback func(host string) error)
	}{
		{"add", 'a', "Add host:", func(o *RunOptions, f func(string) error) { o.OnAddHost = f }},
		{"delete", 'd', "Delete host:", func(o *RunOptions, f func(string) error) { o.OnDeleteHost = f }},
	}
	abandons := []struct {
		name    string
		abandon func(h *tuiHarness)
	}{
		{"submitting an empty host", func(h *tuiHarness) { h.key(tcell.KeyEnter) }},
		{"pressing Escape after typing", func(h *tuiHarness) { h.typeText("8.8.8.8"); h.key(tcell.KeyEscape) }},
	}
	for _, kind := range kinds {
		start := func(t *testing.T) (*tuiHarness, chan string) {
			hosts := make(chan string, 4)
			opts := RunOptions{Targets: []*stats.TargetStats{stats.NewTargetStats("8.8.8.8")}}
			kind.wire(&opts, func(host string) error {
				hosts <- host
				return errors.New("test stop")
			})
			h := startTUI(t, opts, 120, 40)
			h.wait("Ping Monitor")
			return h, hosts
		}
		submit := func(h *tuiHarness, host string) {
			h.press(kind.key)
			h.wait(kind.prompt)
			h.typeText(host)
			h.key(tcell.KeyEnter)
		}

		t.Run(kind.name+" submits the typed host", func(t *testing.T) {
			h, hosts := start(t)
			submit(h, "1.2.3.4")
			if got := recvWithin(t, hosts, "the host callback"); got != "1.2.3.4" {
				t.Fatalf("callback host = %q, want %q", got, "1.2.3.4")
			}
			h.wait("host error: test stop")
			h.quit()
		})
		for _, a := range abandons {
			t.Run(kind.name+" ignores "+a.name, func(t *testing.T) {
				h, hosts := start(t)
				h.press(kind.key)
				h.wait(kind.prompt)
				a.abandon(h)
				submit(h, "1.2.3.4")
				if got := recvWithin(t, hosts, "the host callback"); got != "1.2.3.4" {
					t.Fatalf("first callback host = %q, want only the sentinel %q", got, "1.2.3.4")
				}
				h.quit()
			})
		}
	}
}
