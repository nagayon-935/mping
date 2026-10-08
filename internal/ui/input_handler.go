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

// newInputHandler owns UI state transitions on the event loop. Callback
// operations execute FIFO on the session worker, and results return through
// its cancellable UI mailbox. Stop, restart, and reset can never overtake.
func newInputHandler(d inputHandlerDeps) func(event *tcell.EventKey) *tcell.EventKey {
	state := monitorRunning
	showState := func() {
		if d.footer == nil {
			return
		}
		switch state {
		case monitorRunning:
			d.footer.SetText("Enter Detail | Tab Pane | f Fold | z Max | w Save | a Add | d Del | s Stop | q Quit")
		case monitorStopping:
			d.footer.SetText("Stopping... Press 'S' to restart after stop, 'q' to quit")
		case monitorStopped:
			d.footer.SetText("Stopped. Press 'S' to restart, 'q' to quit, 'R' to reset stats")
		case monitorRestarting:
			d.footer.SetText("Restarting... Press 'q' to quit")
		}
		d.footer.SetTextColor(tcell.ColorYellow)
	}
	submit := func(f func()) bool {
		if d.session.Submit(f) {
			return true
		}
		d.vs.appendLog("[yellow]Operation queue full; please try again[-]")
		return false
	}

	return func(event *tcell.EventKey) *tcell.EventKey {
		// Pass all events through when a text input or modal list is focused.
		switch d.app.GetFocus() {
		case d.addHostInput, d.deleteHostInput:
			return event
		}
		if d.app.GetFocus() == d.table {
			switch event.Key() {
			case tcell.KeyEnter:
				if d.openDetails != nil {
					d.openDetails()
					return nil
				}
			case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
				if d.navigate != nil {
					d.navigate(event.Key())
					return nil
				}
				rowOffset, colOffset := d.table.GetOffset()
				totalRows := *d.rowCount
				visibleRows := tableMaxRows + 1
				maxOffset := totalRows - visibleRows
				if maxOffset < 0 {
					maxOffset = 0
				}

				delta := 0
				switch event.Key() {
				case tcell.KeyUp:
					delta = -1
				case tcell.KeyDown:
					delta = 1
				case tcell.KeyPgUp:
					delta = -tableMaxRows
				case tcell.KeyPgDn:
					delta = tableMaxRows
				}

				rowOffset += delta
				if rowOffset < 0 {
					rowOffset = 0
				} else if rowOffset > maxOffset {
					rowOffset = maxOffset
				}

				d.table.SetOffset(rowOffset, colOffset)
				if d.forceUpdate != nil {
					d.forceUpdate()
				}
				return nil
			}
		}
		switch event.Key() {
		case tcell.KeyTab:
			resetAll := func() {
				d.table.SetBorderColor(tcell.ColorWhite)
				d.vs.errorView.SetBorderColor(tcell.ColorRed)
				d.graphView.SetBorderColor(vividCyan)
				for _, mp := range d.sidePanes {
					mp.setBorderColor(tcell.ColorWhite)
				}
			}
			// Build ordered focus cycle: table → [trace] → [mtr] → [port] → [http] → graph → error → table
			type focusEntry struct {
				enabled  bool
				view     tview.Primitive
				setColor func(tcell.Color)
			}
			focusCycle := []focusEntry{
				{true, d.table, func(c tcell.Color) { d.table.SetBorderColor(c) }},
			}
			for _, mp := range d.sidePanes {
				focusCycle = append(focusCycle, focusEntry{mp.enabled, mp.view, mp.setBorderColor})
			}
			focusCycle = append(focusCycle,
				focusEntry{true, d.graphView, func(c tcell.Color) { d.graphView.SetBorderColor(c) }},
				focusEntry{true, d.vs.errorView, func(c tcell.Color) { d.vs.errorView.SetBorderColor(c) }},
			)
			focused := d.app.GetFocus()
			for i, entry := range focusCycle {
				if entry.enabled && entry.view == focused {
					resetAll()
					for j := 1; j <= len(focusCycle); j++ {
						next := focusCycle[(i+j)%len(focusCycle)]
						if next.enabled {
							d.app.SetFocus(next.view)
							next.setColor(tcell.ColorGreen)
							break
						}
					}
					return nil
				}
			}
			// Fallback: focus table
			resetAll()
			d.app.SetFocus(d.table)
			d.table.SetBorderColor(tcell.ColorGreen)
			return nil
		}

		switch event.Rune() {
		case 'a':
			if d.onAddHost != nil {
				d.pages.SwitchToPage("addHost")
				d.app.SetFocus(d.addHostInput)
				return nil
			}
		case 'd':
			if d.deleteSelected != nil {
				d.deleteSelected()
				return nil
			}
			if d.onDeleteHost != nil {
				d.pages.SwitchToPage("deleteHost")
				d.app.SetFocus(d.deleteHostInput)
				return nil
			}
		case 'q':
			d.session.Stop()
			d.app.Stop()
		case 's':
			if state == monitorRunning {
				state = monitorStopping
				d.vs.appendLog(fmt.Sprintf("[yellow][%s] Stop requested by user[-]", time.Now().Format("15:04:05")))
				if !submit(func() {
					if d.onStop != nil {
						d.onStop()
					}
					d.session.Post(func() {
						if state == monitorStopping {
							state = monitorStopped
							showState()
						}
					})
				}) {
					state = monitorRunning
					break
				}
				showState()
			}
		case 'S':
			if (state == monitorStopping || state == monitorStopped) && d.onRestart != nil {
				previous := state
				state = monitorRestarting
				d.vs.appendLog(fmt.Sprintf("[yellow][%s] Restart requested by user[-]", time.Now().Format("15:04:05")))
				if !submit(func() {
					err := d.onRestart()
					d.session.Post(func() {
						if err != nil {
							state = monitorStopped
							showState()
							d.vs.appendLog(fmt.Sprintf("[red][%s] Restart failed: %v[-]", time.Now().Format("15:04:05"), err))
							return
						}
						state = monitorRunning
						showState()
					})
				}) {
					state = previous
				}
				showState()
			}
		case 'R':
			d.vs.reset()
			if d.onReset != nil {
				submit(d.onReset)
				break
			}
			// Compatibility path for standalone callers. Keep even the
			// counter reset on the same FIFO worker as stop and restart.
			running := state == monitorRunning || state == monitorRestarting
			submit(func() {
				for _, t := range d.targets {
					t.Reset()
				}
				if !running {
					return
				}
				if d.traceEnabled {
					for _, t := range d.targets {
						t.SetTraceHops(nil)
					}
					if d.onResetTrace != nil {
						d.onResetTrace()
					}
				}
				if d.mtrEnabled && d.onResetMTR != nil {
					d.onResetMTR()
				}
				if d.portEnabled && d.onResetPort != nil {
					d.onResetPort()
				}
				if d.httpEnabled && d.onResetHTTP != nil {
					d.onResetHTTP()
				}
			})
		}

		return event
	}
}
