package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// inputHandlerDeps bundles everything the main key handler needs: widgets
// and callbacks that stay fixed for Run()'s lifetime, plus pointers to the
// viewState it shares with tableRenderer: the error log and per-host
// alert/status caches. Both sides observe the same state after the R key
// clears it.
type inputHandlerDeps struct {
	app          *tview.Application
	table        *tview.Table
	addHostInput *tview.InputField
	footer       *tview.TextView
	pages        *tview.Pages

	vs             *viewState
	navigate       func(tcell.Key)
	openDetails    func()
	deleteSelected func()

	onReset          func()
	onStop           func()
	measurementState func() string
	onRestart        func() error
	onAddHost        func(host string) error

	session *uiSession
}

// monitorState describes UI operation progress. The supervisor owns the
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
	// Browser controls and count completion can change the supervisor while
	// this input handler is idle. Never override an operation still queued.
	if h.measurementState != nil && (h.state == monitorRunning || h.state == monitorStopped) {
		state := h.measurementState()
		if state == "stopped" || state == "finished" {
			h.state = monitorStopped
		} else if state == "running" {
			h.state = monitorRunning
		}
	}
	// Pass all events through when a text input is focused.
	if h.app.GetFocus() == h.addHostInput {
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
			}
			return nil
		}
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

func (h *inputHandler) showState() {
	if h.footer == nil {
		return
	}
	switch h.state {
	case monitorRunning:
		h.footer.SetText(footerHelpText)
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
	h.vs.appendLog(queueFullLog)
	return false
}

// stop ('s') stops measuring; only from running.
func (h *inputHandler) stop() {
	if h.state != monitorRunning {
		return
	}
	h.state = monitorStopping
	h.vs.appendLog(fmt.Sprintf("[yellow][%s] Stop requested by user[-]", time.Now().Format(logTimeLayout)))
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
	h.vs.appendLog(fmt.Sprintf("[yellow][%s] Restart requested by user[-]", time.Now().Format(logTimeLayout)))
	if !h.submit(func() {
		err := h.onRestart()
		h.session.Post(func() {
			if err != nil {
				h.state = monitorStopped
				h.showState()
				h.vs.appendLog(fmt.Sprintf("[red][%s] Restart failed: %v[-]", time.Now().Format(logTimeLayout), err))
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

// reset ('R') clears the view state and delegates measurement reset to its owner.
func (h *inputHandler) reset() {
	h.vs.reset()
	if h.onReset != nil {
		h.submit(h.onReset)
	}
}
