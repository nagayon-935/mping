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
	errorView       *tview.TextView
	footer          *tview.TextView
	sidePanes       []*monitorPane
	pages           *tview.Pages
	targets         []*stats.TargetStats

	rowCount         *int
	errorLogs        *[]string
	lastLossTimes    *map[string]time.Time
	alertState       *map[string]alertFlags
	lastPortStatuses *map[string]string
	lastHTTPStatuses *map[string]string

	traceEnabled bool
	mtrEnabled   bool
	portEnabled  bool
	httpEnabled  bool

	onStop       func()
	onRestart    func() error
	onResetTrace func()
	onResetMTR   func()
	onResetPort  func()
	onResetHTTP  func()
	onAddHost    func(host string) error
	onDeleteHost func(host string) error

	closeAppStop func()
}

// newInputHandler returns the SetInputCapture callback for Run()'s
// application. stopRequested is owned entirely by the returned closure
// (nothing outside the key handler reads or writes it), so it lives here
// rather than in inputHandlerDeps.
func newInputHandler(d inputHandlerDeps) func(event *tcell.EventKey) *tcell.EventKey {
	stopRequested := false

	return func(event *tcell.EventKey) *tcell.EventKey {
		// Pass all events through when a text input or modal list is focused.
		switch d.app.GetFocus() {
		case d.addHostInput, d.deleteHostInput:
			return event
		}
		if d.app.GetFocus() == d.table {
			switch event.Key() {
			case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
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
				return nil
			}
		}
		switch event.Key() {
		case tcell.KeyTab:
			resetAll := func() {
				d.table.SetBorderColor(tcell.ColorWhite)
				d.errorView.SetBorderColor(tcell.ColorRed)
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
				focusEntry{true, d.errorView, func(c tcell.Color) { d.errorView.SetBorderColor(c) }},
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
			if d.onDeleteHost != nil {
				d.pages.SwitchToPage("deleteHost")
				d.app.SetFocus(d.deleteHostInput)
				return nil
			}
		case 'q':
			d.closeAppStop() // stop refresh goroutine before screen teardown
			d.app.Stop()
		case 's':
			if !stopRequested {
				stopRequested = true
				appendErrorLog(d.errorLogs, d.errorView, fmt.Sprintf("[yellow][%s] Stop requested by user[-]", time.Now().Format("15:04:05")))
				if d.onStop != nil {
					go d.onStop()
				}
				if d.footer != nil {
					d.footer.SetText("Stopped. Press 'S' to restart, 'q' to quit, 'R' to reset stats")
					d.footer.SetTextColor(tcell.ColorYellow)
				}
			}
		case 'S':
			if stopRequested {
				appendErrorLog(d.errorLogs, d.errorView, fmt.Sprintf("[yellow][%s] Restart requested by user[-]", time.Now().Format("15:04:05")))
				if d.onRestart != nil {
					go func() {
						if err := d.onRestart(); err != nil {
							d.app.QueueUpdateDraw(func() {
								appendErrorLog(d.errorLogs, d.errorView, fmt.Sprintf("[red][%s] Restart failed: %v[-]", time.Now().Format("15:04:05"), err))
							})
							return
						}
						d.app.QueueUpdateDraw(func() {
							stopRequested = false
							if d.footer != nil {
								d.footer.SetText("Tab: Switch Focus | q: Quit | s: Stop ping | R: Reset stats")
								d.footer.SetTextColor(tcell.ColorYellow)
							}
						})
					}()
				}
			}
		case 'R':
			for _, t := range d.targets {
				t.Reset()
			}
			// Also clear error log
			*d.errorLogs = []string{}
			d.errorView.SetText("")
			*d.lastLossTimes = make(map[string]time.Time)
			*d.alertState = make(map[string]alertFlags)
			*d.lastPortStatuses = make(map[string]string)
			*d.lastHTTPStatuses = make(map[string]string)
			if !stopRequested {
				if d.traceEnabled {
					for _, t := range d.targets {
						t.SetTraceHops(nil)
					}
					if d.onResetTrace != nil {
						go d.onResetTrace()
					}
				}
				if d.mtrEnabled && d.onResetMTR != nil {
					go d.onResetMTR()
				}
				if d.portEnabled && d.onResetPort != nil {
					go d.onResetPort()
				}
				if d.httpEnabled && d.onResetHTTP != nil {
					go d.onResetHTTP()
				}
			}
		}
		return event
	}
}
