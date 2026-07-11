package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// wireHostInputs sets the Enter/Escape behavior for the add-host and
// delete-host input fields: Enter invokes the corresponding callback (if
// non-nil and the field isn't empty) and logs any error, Escape clears the
// field; both cases return focus to the footer page and the table.
func wireHostInputs(
	app *tview.Application, table *tview.Table, pages *tview.Pages,
	addHostInput, deleteHostInput *tview.InputField,
	vs *viewState,
	onAddHost, onDeleteHost func(host string) error,
) {
	addHostInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			host := strings.TrimSpace(addHostInput.GetText())
			addHostInput.SetText("")
			if host != "" && onAddHost != nil {
				go func() {
					if err := onAddHost(host); err != nil {
						app.QueueUpdateDraw(func() {
							vs.appendLog(fmt.Sprintf("[red][%s] Add host error: %v[-]",
								time.Now().Format("15:04:05"), err))
						})
					}
				}()
			}
		} else if key == tcell.KeyEscape {
			addHostInput.SetText("")
		}
		pages.SwitchToPage("footer")
		app.SetFocus(table)
	})

	deleteHostInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			host := strings.TrimSpace(deleteHostInput.GetText())
			deleteHostInput.SetText("")
			if host != "" && onDeleteHost != nil {
				go func() {
					if err := onDeleteHost(host); err != nil {
						app.QueueUpdateDraw(func() {
							vs.appendLog(fmt.Sprintf("[red][%s] Delete host error: %v[-]",
								time.Now().Format("15:04:05"), err))
						})
					}
				}()
			}
		} else if key == tcell.KeyEscape {
			deleteHostInput.SetText("")
		}
		pages.SwitchToPage("footer")
		app.SetFocus(table)
	})
}

// startRefreshLoop launches the goroutine that redraws the table on each
// tick, delivers external log/close signals from outside the TUI, and stops
// once the pinger finishes (doneCh) or the app quits (appStop). TD-23③:
// extracted out of Run() so its ~50 lines don't compete with construction
// for reading attention.
func startRefreshLoop(
	app *tview.Application, tr *tableRenderer, footer *tview.TextView,
	interval time.Duration, updateTickerCh chan time.Duration,
	externalLogCh <-chan string, externalCloseCh <-chan struct{}, doneCh chan struct{},
	vs *viewState,
	closeAppStop func(), appStop chan struct{},
) {
	go func() {
		ticker := time.NewTicker(interval / 2)
		if interval < minUIRefresh {
			ticker.Reset(fastUIRefresh)
		}
		defer ticker.Stop()
		for {
			select {
			case newInterval := <-updateTickerCh:
				ticker.Stop()
				if newInterval < minUIRefresh {
					ticker = time.NewTicker(fastUIRefresh)
				} else {
					ticker = time.NewTicker(newInterval / 2)
				}
			case <-ticker.C:
				app.QueueUpdateDraw(tr.update)
			case msg := <-externalLogCh:
				// Deliver external log messages (e.g. watcher validation errors)
				// immediately to the Log pane without waiting for the next tick.
				app.QueueUpdateDraw(func() {
					vs.appendLog(msg)
				})
			case <-externalCloseCh:
				// External reload requested (e.g. YAML file changed).
				app.QueueUpdateDraw(func() {
					vs.appendLog(fmt.Sprintf("[yellow][%s] Reloading configuration...[-]",
						time.Now().Format("15:04:05")))
				})
				closeAppStop()
				app.Stop()
				return
			case <-doneCh:
				// Pinger finished (count limit reached)
				app.QueueUpdateDraw(func() {
					footer.SetText("Finished. Press 'q' to quit, 'R' to reset stats")
					footer.SetTextColor(tcell.ColorGreen)
					tr.update()
				})
				// Stop refreshing since pinger is done
				return
			case <-appStop:
				return
			}
		}
	}()
}

// buildLayout assembles the top-level Flex layout: header, Ping Monitor
// table, enabled side panes (weight 3 alone, 2 when multiple), RTT graph,
// Log pane, and the footer/input pages row.
func buildLayout(header *tview.TextView, tablePane *tview.Flex, sidePanes []*monitorPane, graphView *GraphView, errorView *tview.TextView, pages *tview.Pages) *tview.Flex {
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 2, 0, false).
		AddItem(tablePane, 0, 3, true)

	var activePanes []*tview.Flex
	for _, mp := range sidePanes {
		if mp.enabled && mp.pane != nil {
			activePanes = append(activePanes, mp.pane)
		}
	}
	monitorWeight := 3
	if len(activePanes) > 1 {
		monitorWeight = 2
	}
	for _, pane := range activePanes {
		flex.AddItem(pane, 0, monitorWeight, false)
	}
	flex.AddItem(graphView, 0, 3, false).
		AddItem(errorView, 0, 2, false).
		AddItem(pages, 1, 0, false)

	flex.SetBackgroundColor(tcell.ColorBlack)
	return flex
}
