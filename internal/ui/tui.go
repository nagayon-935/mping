package ui

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

const (
	tableMaxRows  = 10                      // max ping targets shown without scrolling; keeps UI readable at typical terminal heights
	minUIRefresh  = 200 * time.Millisecond  // minimum refresh to avoid flicker at very short ping intervals
	fastUIRefresh = 100 * time.Millisecond  // UI refresh rate when ping interval < minUIRefresh
)

var newApplication = tview.NewApplication

func Run(targets []*stats.TargetStats, interval, timeout time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, asnEnabled bool, onStop func(), onRestart func() error, onSettingsChange func(interval, timeout time.Duration, packetSize int) error, onResetTrace func(), onResetPort func()) error {
	app := newApplication()
	table := tview.NewTable().
		SetBorders(true).
		SetSelectable(false, false).
		SetFixed(1, 1)

	// Use custom GraphView
	graphView := NewGraphView(targets, interval)
	graphView.SetBorder(true).SetTitle(" RTT Graphs ").SetTitleColor(vividCyan).SetBorderColor(vividCyan)
	graphView.SetBackgroundColor(tcell.ColorBlack)

	errorView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true) // Ensure long messages wrap
	errorView.SetBorder(true).SetTitle(" Log ").SetTitleColor(vividRed).SetBorderColor(vividRed)
	errorView.SetBackgroundColor(tcell.ColorBlack)

	// Set black background and darkgray borders
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetBorderColor(tcell.ColorWhite)
	table.SetBordersColor(tcell.ColorWhite)


	// Columns: Src IP, Dst IP, ASN, Success, Loss, Loss Ratio, RTT, Avg, Jitter, Size, MTU, TTL, Error, Last Loss
	fullHeaders := []string{"Src IP", "Dst IP"}
	fullAligns := []int{tview.AlignLeft, tview.AlignLeft}
	baseWidths := []int{6, 6}
	minWidths := []int{4, 8}
	maxWidths := []int{45, 60}

	if asnEnabled {
		fullHeaders = append(fullHeaders, "ASN")
		fullAligns = append(fullAligns, tview.AlignRight)
		baseWidths = append(baseWidths, 4)
		minWidths = append(minWidths, 4)
		maxWidths = append(maxWidths, 15)
	}

	fullHeaders = append(fullHeaders, "Success", "Loss", "Loss Ratio", "RTT", "Avg", "Jitter", "Size", "MTU", "TTL", "Error", "Last Loss")
	fullAligns = append(fullAligns, tview.AlignRight, tview.AlignRight, tview.AlignRight,
		tview.AlignRight, tview.AlignRight, tview.AlignRight, // RTTs
		tview.AlignRight, tview.AlignRight, tview.AlignRight, tview.AlignLeft, tview.AlignLeft)
	baseWidths = append(baseWidths, 8, 7, 10, 10, 10, 10, 6, 6, 5, 30, 15)
	minWidths = append(minWidths, 5, 4, 6, 7, 7, 7, 4, 4, 3, 8, 8)
	maxWidths = append(maxWidths, 10, 10, 12, 12, 12, 12, 8, 8, 6, 30, 18)

	// Update error width index
	errorIdx := len(fullHeaders) - 2
	baseWidths[errorIdx] = calcInitialTableErrorWidth(targets, fullHeaders[errorIdx], baseWidths[errorIdx])
	maxWidths[errorIdx] = baseWidths[errorIdx]

	headerColor := tcell.ColorYellow
	rowColor := tcell.ColorWhite

	// Recalculate dynamic column widths based on current output text.
	calcColumnWidths := func() []int {
		widths := append([]int(nil), baseWidths...)
		dynamicCols := []int{0, 1}
		if asnEnabled {
			dynamicCols = append(dynamicCols, 2)
		}
		for _, c := range dynamicCols {
			maxWidth := runewidth.StringWidth(fullHeaders[c])
			for _, t := range targets {
				view := t.GetView()
				value := ""
				switch c {
				case 0:
					value = displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
				case 1:
					if view.Host != view.IP {
						value = fmt.Sprintf("%s (%s)", view.Host, view.IP)
					} else {
						value = view.IP
					}
				case 2:
					if asnEnabled {
						value = view.ASN
					}
				}
				if w := runewidth.StringWidth(value); w > maxWidth {
					maxWidth = w
				}
			}
			widths[c] = maxWidth
		}
		return widths
	}

	widths := calcColumnWidths()
	activeHeaders := append([]string(nil), fullHeaders...)
	activeAligns := append([]int(nil), fullAligns...)
	rowCount := len(targets) + 1
	compactLayout := false
	filter := ""

	// Filter input
	filterInput := tview.NewInputField().
		SetLabel(" Filter: ").
		SetFieldBackgroundColor(tcell.ColorBlack).
		SetFieldTextColor(tcell.ColorWhite).
		SetLabelColor(tcell.ColorYellow)

	// Config form
	var configForm *tview.Form
	configForm = tview.NewForm()
	configForm.SetBorder(true).SetTitle(" Settings ").SetTitleColor(tcell.ColorYellow)
	configForm.SetBackgroundColor(tcell.ColorBlack)
	configForm.SetFieldBackgroundColor(tcell.ColorBlack)
	configForm.SetFieldTextColor(tcell.ColorWhite)

	isInteger := func(textToCheck string, lastChar rune) bool {
		if textToCheck == "" {
			return true
		}
		_, err := strconv.Atoi(textToCheck)
		return err == nil
	}

	intervalField := tview.NewInputField().SetLabel("Interval (ms)").SetText(fmt.Sprintf("%d", interval.Milliseconds())).SetAcceptanceFunc(isInteger)
	timeoutField := tview.NewInputField().SetLabel("Timeout (ms)").SetText(fmt.Sprintf("%d", timeout.Milliseconds())).SetAcceptanceFunc(isInteger)
	packetSizeField := tview.NewInputField().SetLabel("Packet Size (bytes)").SetText(fmt.Sprintf("%d", packetSize)).SetAcceptanceFunc(isInteger)

	configForm.AddFormItem(intervalField).
		AddFormItem(timeoutField).
		AddFormItem(packetSizeField)

	tablePane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true)
	tablePane.SetBorder(true).SetTitle(" Ping Monitor ").SetBorderColor(tcell.ColorWhite)

	// Cached column widths: recalculate only when the terminal width changes.
	var cachedWidths []int
	lastTermWidth := -1

	calcColumnWidthsCached := func() []int {
		_, _, curTermWidth, _ := tablePane.GetInnerRect()
		if cachedWidths == nil || curTermWidth != lastTermWidth {
			cachedWidths = calcColumnWidths()
			lastTermWidth = curTermWidth
		}
		return cachedWidths
	}

	var traceView *tview.TextView
	var tracePane *tview.Flex
	setTraceBorderColor := func(_ tcell.Color) {}

	if traceEnabled {
		traceView = tview.NewTextView().
			SetDynamicColors(true).
			SetScrollable(true).
			SetWrap(false)
		traceView.SetBackgroundColor(tcell.ColorBlack)

		tracePane = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(traceView, 0, 1, true)
		tracePane.SetBorder(true).SetBorderColor(tcell.ColorWhite)

		traceBorderColor := tcell.ColorWhite
		setTraceBorderColor = func(c tcell.Color) {
			traceBorderColor = c
			tracePane.SetBorderColor(c)
		}
		tracePane.SetDrawFunc(makeDoubleBorderDrawFunc(" Traceroute Monitor ", &traceBorderColor))
	}

	// Port Monitor pane
	var portView *tview.TextView
	var portPane *tview.Flex
	setPortBorderColor := func(_ tcell.Color) {}

	if portEnabled {
		portView = tview.NewTextView().
			SetDynamicColors(true).
			SetScrollable(true).
			SetWrap(false)
		portView.SetBackgroundColor(tcell.ColorBlack)

		portPane = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(portView, 0, 1, true)
		portPane.SetBorder(true).SetBorderColor(tcell.ColorWhite)

		portBorderColor := tcell.ColorWhite
		setPortBorderColor = func(c tcell.Color) {
			portBorderColor = c
			portPane.SetBorderColor(c)
		}
		portPane.SetDrawFunc(makeDoubleBorderDrawFunc(" Port Monitor ", &portBorderColor))
	}

	// Error log state
	errorLogs := []string{}
	lastLossTimes := make(map[string]time.Time)
	alertState := make(map[string]alertFlags)
	lastPortStatuses := make(map[string]string)

	for _, line := range initialLogs {
		appendErrorLog(&errorLogs, errorView, line)
	}

	header := tview.NewTextView().
		SetText(fmt.Sprintf("MPING - Multi Ping Tool | Interval: %dms", interval.Milliseconds())).
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorGreen).
		SetWrap(false)
	header.SetBackgroundColor(tcell.ColorBlack)

	var footer *tview.TextView
	footer = tview.NewTextView().
		SetText("Tab: Switch Focus | /: Filter | ,: Settings | q: Quit | s: Stop ping | R: Reset stats").
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorYellow).
		SetWrap(false)
	footer.SetBackgroundColor(tcell.ColorBlack)

	// Center configForm
	configFormContainer := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(configForm, 15, 1, true).
			AddItem(nil, 0, 1, false), 60, 1, true).
		AddItem(nil, 0, 1, false)

	pages := tview.NewPages().
		AddPage("footer", footer, true, true).
		AddPage("filter", filterInput, true, false).
		AddPage("settings", configFormContainer, true, false)

	updateTickerCh := make(chan time.Duration, 1)
	var updateTable func()

	configForm.AddButton("Apply", func() {
		newIntMs, _ := strconv.Atoi(intervalField.GetText())
		newTimeoutMs, _ := strconv.Atoi(timeoutField.GetText())
		newPacketSize, _ := strconv.Atoi(packetSizeField.GetText())

		newInterval := time.Duration(newIntMs) * time.Millisecond
		newTimeout := time.Duration(newTimeoutMs) * time.Millisecond

		if onSettingsChange != nil {
			// Reset all statistics
			for _, t := range targets {
				t.Reset()
			}
			// Clear UI state
			errorLogs = []string{}
			errorView.SetText("")
			lastLossTimes = make(map[string]time.Time)
			alertState = make(map[string]alertFlags)
			lastPortStatuses = make(map[string]string)

			if err := onSettingsChange(newInterval, newTimeout, newPacketSize); err != nil {
				appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[red]Settings change failed: %v[-]", err))
			} else {
				interval = newInterval
				packetSize = newPacketSize
				updateTickerCh <- newInterval
				header.SetText(fmt.Sprintf("MPING - Multi Ping Tool | Interval: %dms", interval.Milliseconds()))
				appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[green]Settings applied: Interval=%dms, Timeout=%dms, PacketSize=%d bytes[-]", newIntMs, newTimeoutMs, newPacketSize))
			}
		}
		pages.SwitchToPage("footer")
		app.SetFocus(table)
		updateTable()
	})

	configForm.AddButton("Cancel", func() {
		pages.SwitchToPage("footer")
		app.SetFocus(table)
	})

	configForm.SetCancelFunc(func() {
		pages.SwitchToPage("footer")
		app.SetFocus(table)
	})

	filterInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			filter = filterInput.GetText()
		} else if key == tcell.KeyEscape {
			filterInput.SetText(filter) // Restore previous filter
		}
		pages.SwitchToPage("footer")
		app.SetFocus(table)
		updateTable()
	})

	stopRequested := false
	updateTable = func() {
		table.Clear()

		var filteredTargets []*stats.TargetStats
		for _, t := range targets {
			if matchesFilter(t.GetView(), filter) {
				filteredTargets = append(filteredTargets, t)
			}
		}

		title := " Ping Monitor "
		if filter != "" {
			title = fmt.Sprintf(" Ping Monitor (Filter: %q, showing %d/%d) ", filter, len(filteredTargets), len(targets))
		}
		tablePane.SetTitle(title)

		_, _, availableTableWidth, _ := tablePane.GetInnerRect()
		availableColumnsWidth := availableTableWidth - (len(fullHeaders) + 1)
		if availableColumnsWidth < 0 {
			availableColumnsWidth = 0
		}

		updatedWidths := calcColumnWidthsCached()
		dynamicMaxWidths := append([]int(nil), maxWidths...)
		dynamicCols := []int{0, 1}
		if asnEnabled {
			dynamicCols = append(dynamicCols, 2)
		}
		for _, c := range dynamicCols {
			if updatedWidths[c] > dynamicMaxWidths[c] {
				dynamicMaxWidths[c] = updatedWidths[c]
			}
		}
		fitted, ok := fitWidthsToAvailable(updatedWidths, minWidths, dynamicMaxWidths, availableColumnsWidth)

		compact := buildCompactLayout(filteredTargets, packetSize, sourceIPv4, sourceIPv6, baseWidths[errorIdx+1])
		compactRows := compact.rows
		compactDesired := compact.desired
		compactHeaders := compact.headers
		compactAligns := compact.aligns
		compactMin := compact.min
		compactMax := compact.max
		compactAvailableColumnsWidth := availableTableWidth - (len(compactHeaders) + 1)
		if compactAvailableColumnsWidth < 0 {
			compactAvailableColumnsWidth = 0
		}
		compactWidths, compactOK := fitWidthsToAvailable(compactDesired, compactMin, compactMax, compactAvailableColumnsWidth)

		if ok {
			compactLayout = false
			widths = fitted
			activeHeaders = append([]string(nil), fullHeaders...)
			activeAligns = append([]int(nil), fullAligns...)
			rowCount = len(filteredTargets) + 1
		} else if compactOK {
			compactLayout = true
			widths = compactWidths
			activeHeaders = append([]string(nil), compactHeaders...)
			activeAligns = append([]int(nil), compactAligns...)
			rowCount = len(compactRows) + 1
		}

		// Header
		for i, h := range activeHeaders {
			text := formatCellText(h, widths[i], activeAligns[i])
			cell := tview.NewTableCell(text).
				SetBackgroundColor(tcell.ColorBlack).
				SetTextColor(headerColor).
				SetAttributes(tcell.AttrBold).
				SetSelectable(false).
				SetAlign(activeAligns[i])
			if i == len(activeHeaders)-1 {
				cell.SetExpansion(1)
			}
			table.SetCell(0, i, cell)
		}

		pickCompact := func(right, left string) string {
			if right != "" {
				return right
			}
			return left
		}
		if compactLayout {
			for i, r := range compactRows {
				row := i + 1
				values := []string{
					pickCompact(r.hostR, r.hostL),
					pickCompact(r.pathR, r.pathL),
					pickCompact(r.statR, r.statL),
					pickCompact(r.errR, r.errL),
				}
				cells := buildCompactRowCells(values, widths, activeAligns, rowColor)
				for c, cell := range cells {
					table.SetCell(row, c, cell)
				}
			}
		}

		// Update table rows AND Error logs
		displayIdx := 1
		for _, t := range targets {
			view := t.GetView()

			// Check for new errors
			if !view.LastLossTime.IsZero() {
				lastTime, exists := lastLossTimes[view.Host]
				if !exists || view.LastLossTime.After(lastTime) {
					// New error detected
					lastLossTimes[view.Host] = view.LastLossTime
					rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
					msg := buildErrorLogMessage(view, rowSourceIP, view.LastError, view.LastLossTime)
					appendErrorLog(&errorLogs, errorView, msg)
				}
			}

			if !matchesFilter(view, filter) {
				continue
			}

			if compactLayout {
				continue
			}

			row := displayIdx
			displayIdx++
			cols, rowSourceIP, lossRate := buildFullColumns(view, sourceIPv4, sourceIPv6, packetSize, asnEnabled)

			// Alert logs on red thresholds
			state := alertState[view.Host]
			now := time.Now()
			state, msgs := updateAlertState(view, rowSourceIP, lossRate, now, state)
			for _, msg := range msgs {
				appendErrorLog(&errorLogs, errorView, msg)
			}
			alertState[view.Host] = state

			cells := buildFullRowCells(cols, widths, fullAligns, lossRate, view.LastRTT, view.Jitter, rowColor, asnEnabled)
			for c, cell := range cells {
				table.SetCell(row, c, cell)
			}
		}

		if traceEnabled && traceView != nil {
			_, _, availW, _ := traceView.GetInnerRect()
			traceView.SetText(renderTracerouteTable(targets, availW))
		}

		if portEnabled && portView != nil {
			_, _, availW, _ := portView.GetInnerRect()
			portView.SetText(renderPortMonitorTable(targets, availW, lastPortStatuses, &errorLogs, errorView))
		}
	}

	appStop := make(chan struct{})
	var appStopOnce sync.Once
	closeAppStop := func() { appStopOnce.Do(func() { close(appStop) }) }

	// Keys
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if app.GetFocus() == filterInput || app.GetFocus() == configForm {
			return event
		}
		if app.GetFocus() == table {
			switch event.Key() {
			case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
				rowOffset, colOffset := table.GetOffset()
				totalRows := rowCount
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

				table.SetOffset(rowOffset, colOffset)
				return nil
			}
		}
		switch event.Key() {
		case tcell.KeyTab:
			resetAll := func() {
				table.SetBorderColor(tcell.ColorWhite)
				errorView.SetBorderColor(tcell.ColorRed)
				graphView.SetBorderColor(vividCyan)
				setTraceBorderColor(tcell.ColorWhite)
				setPortBorderColor(tcell.ColorWhite)
			}
			if app.GetFocus() == table {
				if traceEnabled && traceView != nil {
					resetAll()
					app.SetFocus(traceView)
					setTraceBorderColor(tcell.ColorGreen)
				} else if portEnabled && portView != nil {
					resetAll()
					app.SetFocus(portView)
					setPortBorderColor(tcell.ColorGreen)
				} else {
					resetAll()
					app.SetFocus(graphView)
					graphView.SetBorderColor(tcell.ColorGreen)
				}
			} else if traceEnabled && traceView != nil && app.GetFocus() == traceView {
				if portEnabled && portView != nil {
					resetAll()
					app.SetFocus(portView)
					setPortBorderColor(tcell.ColorGreen)
				} else {
					resetAll()
					app.SetFocus(graphView)
					graphView.SetBorderColor(tcell.ColorGreen)
				}
			} else if portEnabled && portView != nil && app.GetFocus() == portView {
				resetAll()
				app.SetFocus(graphView)
				graphView.SetBorderColor(tcell.ColorGreen)
			} else if app.GetFocus() == graphView {
				resetAll()
				app.SetFocus(errorView)
				errorView.SetBorderColor(tcell.ColorGreen)
			} else {
				resetAll()
				app.SetFocus(table)
				table.SetBorderColor(tcell.ColorGreen)
			}
			return nil
		}

		switch event.Rune() {
		case '/':
			pages.SwitchToPage("filter")
			app.SetFocus(filterInput)
			return nil
		case ',':
			pages.SwitchToPage("settings")
			app.SetFocus(configForm)
			return nil
		case 'q':
			closeAppStop() // stop refresh goroutine before screen teardown
			app.Stop()
		case 's':
			if !stopRequested {
				stopRequested = true
				appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[yellow][%s] Stop requested by user[-]", time.Now().Format("15:04:05")))
				if onStop != nil {
					go onStop()
				}
				if footer != nil {
					footer.SetText("Stopped. Press 'S' to restart, 'q' to quit, 'R' to reset stats")
					footer.SetTextColor(tcell.ColorYellow)
				}
			}
		case 'S':
			if stopRequested {
				appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[yellow][%s] Restart requested by user[-]", time.Now().Format("15:04:05")))
				if onRestart != nil {
					go func() {
						if err := onRestart(); err != nil {
							app.QueueUpdateDraw(func() {
								appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[red][%s] Restart failed: %v[-]", time.Now().Format("15:04:05"), err))
							})
							return
						}
						app.QueueUpdateDraw(func() {
							stopRequested = false
							if footer != nil {
								footer.SetText("Tab: Switch Focus | q: Quit | s: Stop ping | R: Reset stats")
								footer.SetTextColor(tcell.ColorYellow)
							}
						})
					}()
				}
			}
		case 'R':
			for _, t := range targets {
				t.Reset()
			}
			// Also clear error log
			errorLogs = []string{}
			errorView.SetText("")
			lastLossTimes = make(map[string]time.Time)
			alertState = make(map[string]alertFlags)
			lastPortStatuses = make(map[string]string)
			if !stopRequested {
				if traceEnabled {
					for _, t := range targets {
						t.SetTraceHops(nil)
					}
					if onResetTrace != nil {
						go onResetTrace()
					}
				}
				if portEnabled && onResetPort != nil {
					go onResetPort()
				}
			}
		}
		return event
	})
	// Refresh loop
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
				app.QueueUpdateDraw(updateTable)
			case <-doneCh:
				// Pinger finished (count limit reached)
				app.QueueUpdateDraw(func() {
					footer.SetText("Finished. Press 'q' to quit, 'R' to reset stats")
					footer.SetTextColor(tcell.ColorGreen)
					updateTable()
				})
				// Stop refreshing since pinger is done
				return
			case <-appStop:
				return
			}
		}
	}()

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 2, 0, false).
		AddItem(tablePane, 0, 3, true).  // Table: 3 parts
		AddItem(graphView, 0, 3, false). // Graph: 3 parts
		AddItem(errorView, 0, 2, false). // Logs: 2 parts
		AddItem(pages, 1, 0, false)

	if traceEnabled && tracePane != nil && portEnabled && portPane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(tracePane, 0, 2, false). // Trace: 2 parts
			AddItem(portPane, 0, 2, false).  // Port: 2 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(pages, 1, 0, false)
	} else if traceEnabled && tracePane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(tracePane, 0, 3, false). // Trace: 3 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(pages, 1, 0, false)
	} else if portEnabled && portPane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(portPane, 0, 3, false).  // Port: 3 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(pages, 1, 0, false)
	}

	flex.SetBackgroundColor(tcell.ColorBlack)

	err := app.SetRoot(flex, true).Run()
	closeAppStop() // fallback: ensure goroutine stops even on non-interactive exit
	return err
}
