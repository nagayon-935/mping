package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// inputHandlerDeps bundles everything the main key handler needs: widgets
// and callbacks that stay fixed for Run()'s lifetime, plus pointers to the
// render state it shares with updateTable (row count, error log, per-host
// alert/status caches) — pointers because both sides must observe the same
// reassignment (e.g. the 'R' key resetting errorLogs to a fresh slice).
//
// TD-23②: extracted out of Run()'s ~170-line app.SetInputCapture closure so
// the key-handling logic lives in its own file, independently readable from
// pane/table construction.
type inputHandlerDeps struct {
	app             *tview.Application
	table           *tview.Table
	addHostInput    *tview.InputField
	deleteHostInput *tview.InputField
	graphView       *GraphView
	footer          *tview.TextView
	sidePanes       []*monitorPane
	pages           *tview.Pages
	targets         []*stats.TargetStats

	rowCount *int
	vs       *viewState
	// forceUpdate re-renders the table immediately after a scroll key
	// changes the offset, rather than waiting for the next (possibly
	// dirty-gated, see run_support.go's shouldRedraw) refresh tick — table
	// rows are now rendered only within the visible scroll window, so
	// without this a scroll could momentarily show blank rows until the
	// next tick populates them.
	forceUpdate    func()
	navigate       func(tcell.Key)
	openDetails    func()
	deleteSelected func()

	traceEnabled bool
	mtrEnabled   bool
	portEnabled  bool
	httpEnabled  bool

	onReset      func()
	onStop       func()
	onRestart    func() error
	onResetTrace func()
	onResetMTR   func()
	onResetPort  func()
	onResetHTTP  func()
	onAddHost    func(host string) error
	onDeleteHost func(host string) error

	session *uiSession
}

// These states describe UI operation progress. The supervisor owns the
// actual measurement state: stopping/restarting last until its callback
// completes. S during stopping is queued after stop; repeated S while
// restarting is ignored. R leaves the running/stopped state unchanged.
type monitorState uint8

const (
	monitorRunning monitorState = iota
	monitorStopping
	monitorStopped
	monitorRestarting
)

// inputHandler owns UI state transitions on the event loop. Callback
// operations execute FIFO on the session worker, and results return through
// its cancellable UI mailbox. Stop, restart, and reset can never overtake.
type inputHandler struct {
	inputHandlerDeps
	state monitorState
}

// newInputHandler returns the main key handler for Run's input capture.
func newInputHandler(d inputHandlerDeps) func(event *tcell.EventKey) *tcell.EventKey {
	h := &inputHandler{inputHandlerDeps: d, state: monitorRunning}
	return h.handle
}

func (h *inputHandler) handle(event *tcell.EventKey) *tcell.EventKey {
	// Pass all events through when a text input is focused.
	switch h.app.GetFocus() {
	case h.addHostInput, h.deleteHostInput:
		return event
	}
	if h.app.GetFocus() == h.table {
		switch event.Key() {
		case tcell.KeyEnter:
			if h.openDetails != nil {
				h.openDetails()
				return nil
			}
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
			if h.navigate != nil {
				h.navigate(event.Key())
			} else {
				h.scrollTable(event.Key())
			}
			return nil
		}
	}
	if event.Key() == tcell.KeyTab {
		h.cycleFocus()
		return nil
	}

	switch event.Rune() {
	case 'a':
		if h.onAddHost != nil {
			h.pages.SwitchToPage("addHost")
			h.app.SetFocus(h.addHostInput)
			return nil
		}
	case 'd':
		if h.deleteSelected != nil {
			h.deleteSelected()
			return nil
		}
		if h.onDeleteHost != nil {
			h.pages.SwitchToPage("deleteHost")
			h.app.SetFocus(h.deleteHostInput)
			return nil
		}
	case 'q':
		h.session.Stop()
		h.app.Stop()
	case 's':
		h.stop()
	case 'S':
		h.restart()
	case 'R':
		h.reset()
	}
	return event
}

// scrollTable moves the table's row offset when no selection-based
// navigation is wired (standalone callers).
func (h *inputHandler) scrollTable(key tcell.Key) {
	rowOffset, colOffset := h.table.GetOffset()
	maxOffset := max(*h.rowCount-(tableMaxRows+1), 0)
	switch key {
	case tcell.KeyUp:
		rowOffset--
	case tcell.KeyDown:
		rowOffset++
	case tcell.KeyPgUp:
		rowOffset -= tableMaxRows
	case tcell.KeyPgDn:
		rowOffset += tableMaxRows
	}
	h.table.SetOffset(min(max(rowOffset, 0), maxOffset), colOffset)
	if h.forceUpdate != nil {
		h.forceUpdate()
	}
}

// cycleFocus moves focus to the next enabled pane:
// table → [trace] → [mtr] → [port] → [http] → graph → log → table.
func (h *inputHandler) cycleFocus() {
	type focusEntry struct {
		enabled  bool
		view     tview.Primitive
		setColor func(tcell.Color)
	}
	cycle := []focusEntry{{true, h.table, func(c tcell.Color) { h.table.SetBorderColor(c) }}}
	for _, mp := range h.sidePanes {
		cycle = append(cycle, focusEntry{mp.enabled, mp.view, mp.setBorderColor})
	}
	cycle = append(cycle,
		focusEntry{true, h.graphView, func(c tcell.Color) { h.graphView.SetBorderColor(c) }},
		focusEntry{true, h.vs.errorView, func(c tcell.Color) { h.vs.errorView.SetBorderColor(c) }},
	)
	h.table.SetBorderColor(tcell.ColorWhite)
	h.vs.errorView.SetBorderColor(tcell.ColorRed)
	h.graphView.SetBorderColor(vividCyan)
	for _, mp := range h.sidePanes {
		mp.setBorderColor(tcell.ColorWhite)
	}
	focused := h.app.GetFocus()
	for i, entry := range cycle {
		if !entry.enabled || entry.view != focused {
			continue
		}
		for j := 1; j <= len(cycle); j++ {
			if next := cycle[(i+j)%len(cycle)]; next.enabled {
				h.app.SetFocus(next.view)
				next.setColor(tcell.ColorGreen)
				return
			}
		}
		return
	}
	// Focus is somewhere outside the cycle: start over at the table.
	h.app.SetFocus(h.table)
	h.table.SetBorderColor(tcell.ColorGreen)
}

func (h *inputHandler) showState() {
	if h.footer == nil {
		return
	}
	switch h.state {
	case monitorRunning:
		h.footer.SetText("Enter Detail | Tab Pane | f Fold | z Max | w Save | a Add | d Del | s Stop | q Quit")
	case monitorStopping:
		h.footer.SetText("Stopping... Press 'S' to restart after stop, 'q' to quit")
	case monitorStopped:
		h.footer.SetText("Stopped. Press 'S' to restart, 'q' to quit, 'R' to reset stats")
	case monitorRestarting:
		h.footer.SetText("Restarting... Press 'q' to quit")
	}
	h.footer.SetTextColor(tcell.ColorYellow)
}

func (h *inputHandler) submit(f func()) bool {
	if h.session.Submit(f) {
		return true
	}
	h.vs.appendLog("[yellow]Operation queue full; please try again[-]")
	return false
}

// stop ('s') stops measuring; only from running.
func (h *inputHandler) stop() {
	if h.state != monitorRunning {
		return
	}
	h.state = monitorStopping
	h.vs.appendLog(fmt.Sprintf("[yellow][%s] Stop requested by user[-]", time.Now().Format("15:04:05")))
	if !h.submit(func() {
		if h.onStop != nil {
			h.onStop()
		}
		h.session.Post(func() {
			if h.state == monitorStopping {
				h.state = monitorStopped
				h.showState()
			}
		})
	}) {
		h.state = monitorRunning
		return
	}
	h.showState()
}

// restart ('S') resumes measuring after a stop, queued behind it if the stop
// is still in progress.
func (h *inputHandler) restart() {
	if (h.state != monitorStopping && h.state != monitorStopped) || h.onRestart == nil {
		return
	}
	previous := h.state
	h.state = monitorRestarting
	h.vs.appendLog(fmt.Sprintf("[yellow][%s] Restart requested by user[-]", time.Now().Format("15:04:05")))
	if !h.submit(func() {
		err := h.onRestart()
		h.session.Post(func() {
			if err != nil {
				h.state = monitorStopped
				h.showState()
				h.vs.appendLog(fmt.Sprintf("[red][%s] Restart failed: %v[-]", time.Now().Format("15:04:05"), err))
				return
			}
			h.state = monitorRunning
			h.showState()
		})
	}) {
		h.state = previous
	}
	h.showState()
}

// reset ('R') clears statistics and restarts the running monitors.
func (h *inputHandler) reset() {
	h.vs.reset()
	if h.onReset != nil {
		h.submit(h.onReset)
		return
	}
	// Compatibility path for standalone callers. Keep even the counter
	// reset on the same FIFO worker as stop and restart.
	running := h.state == monitorRunning || h.state == monitorRestarting
	h.submit(func() {
		for _, t := range h.targets {
			t.Reset()
		}
		if !running {
			return
		}
		if h.traceEnabled {
			for _, t := range h.targets {
				t.SetTraceHops(nil)
			}
			if h.onResetTrace != nil {
				h.onResetTrace()
			}
		}
		if h.mtrEnabled && h.onResetMTR != nil {
			h.onResetMTR()
		}
		if h.portEnabled && h.onResetPort != nil {
			h.onResetPort()
		}
		if h.httpEnabled && h.onResetHTTP != nil {
			h.onResetHTTP()
		}
	})
}
