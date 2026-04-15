package ui

import (
	"fmt"
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

func Run(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error, onResetTrace func(), onResetPort func()) error {
	// Define vivid colors
	vividRed := tcell.NewRGBColor(255, 0, 0)
	vividCyan := tcell.NewRGBColor(0, 255, 255)

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
	errorView.SetBorder(true).SetTitle(" Log ").SetTitleColor(tcell.ColorRed).SetBorderColor(tcell.ColorRed)
	errorView.SetBackgroundColor(tcell.ColorBlack)

	// Set black background and darkgray borders
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetBorderColor(tcell.ColorWhite)
	table.SetBordersColor(tcell.ColorWhite)


	// Columns: Src IP, Dst IP, ASN, Success, Loss, Loss Ratio, RTT, Avg, Jitter, Size, MTU, TTL, Error, Last Loss
	fullHeaders := []string{"Src IP", "Dst IP", "ASN", "Success", "Loss", "Loss Ratio", "RTT", "Avg", "Jitter", "Size", "MTU", "TTL", "Error", "Last Loss"}
	fullAligns := []int{
		tview.AlignLeft, tview.AlignLeft, tview.AlignLeft, tview.AlignRight, tview.AlignRight, tview.AlignRight,
		tview.AlignRight, tview.AlignRight, tview.AlignRight, // RTTs
		tview.AlignRight, tview.AlignRight, tview.AlignRight, tview.AlignLeft, tview.AlignLeft,
	}
	// Src IP / Dst IP / ASN are dynamically resized from the rendered content.
	// Error width is fixed at startup to prevent table size jumps when new errors arrive.
	baseWidths := []int{6, 6, 4, 8, 7, 10, 10, 10, 10, 6, 6, 5, 30, 15}
	baseWidths[12] = calcInitialTableErrorWidth(targets, fullHeaders[12], baseWidths[12])
	minWidths := []int{4, 8, 4, 5, 4, 6, 7, 7, 7, 4, 4, 3, 8, 8}
	maxWidths := []int{45, 60, 15, 10, 10, 12, 12, 12, 12, 8, 8, 6, baseWidths[12], 18}

	headerColor := tcell.ColorYellow
	rowColor := tcell.ColorWhite

	// Recalculate dynamic column widths based on current output text.
	calcColumnWidths := func() []int {
		widths := append([]int(nil), baseWidths...)
		for _, c := range []int{0, 1, 2} {
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
					value = view.ASN
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

	var footer *tview.TextView
	stopRequested := false

	updateTable := func() {
		table.Clear()

		_, _, availableTableWidth, _ := tablePane.GetInnerRect()
		availableColumnsWidth := availableTableWidth - (len(fullHeaders) + 1)
		if availableColumnsWidth < 0 {
			availableColumnsWidth = 0
		}

		updatedWidths := calcColumnWidthsCached()
		dynamicMaxWidths := append([]int(nil), maxWidths...)
		for _, c := range []int{0, 1, 2} {
			if updatedWidths[c] > dynamicMaxWidths[c] {
				dynamicMaxWidths[c] = updatedWidths[c]
			}
		}
		fitted, ok := fitWidthsToAvailable(updatedWidths, minWidths, dynamicMaxWidths, availableColumnsWidth)

		compact := buildCompactLayout(targets, packetSize, sourceIPv4, sourceIPv6, baseWidths[12])
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
			rowCount = len(targets) + 1
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
				cells := buildCompactRowCells(values, widths, activeAligns, vividRed, rowColor)
				for c, cell := range cells {
					table.SetCell(row, c, cell)
				}
			}
		}

		// Update table rows AND Error logs
		for i, t := range targets {
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

			if compactLayout {
				continue
			}

			row := i + 1
			cols, rowSourceIP, lossRate := buildFullColumns(view, sourceIPv4, sourceIPv6, packetSize)

			// Alert logs on red thresholds
			state := alertState[view.Host]
			now := time.Now()
			state, msgs := updateAlertState(view, rowSourceIP, lossRate, now, state)
			for _, msg := range msgs {
				appendErrorLog(&errorLogs, errorView, msg)
			}
			alertState[view.Host] = state

			cells := buildFullRowCells(cols, widths, fullAligns, lossRate, view.LastRTT, view.Jitter, vividRed, rowColor)
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

	header := tview.NewTextView().
		SetText(fmt.Sprintf("MPING - Multi Ping Tool | Interval: %dms", interval.Milliseconds())).
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorGreen).
		SetWrap(false)
	header.SetBackgroundColor(tcell.ColorBlack)

	footer = tview.NewTextView().
		SetText("Tab: Switch Focus | q: Quit | s: Stop ping | R: Reset stats").
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorYellow).
		SetWrap(false)
	footer.SetBackgroundColor(tcell.ColorBlack)

	// Refresh loop
	go func() {
		ticker := time.NewTicker(interval / 2)
		if interval < minUIRefresh {
			ticker.Reset(fastUIRefresh)
		}
		defer ticker.Stop()
		for {
			select {
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
		AddItem(footer, 2, 0, false)

	if traceEnabled && tracePane != nil && portEnabled && portPane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(tracePane, 0, 2, false). // Trace: 2 parts
			AddItem(portPane, 0, 2, false).  // Port: 2 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(footer, 2, 0, false)
	} else if traceEnabled && tracePane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(tracePane, 0, 3, false). // Trace: 3 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(footer, 2, 0, false)
	} else if portEnabled && portPane != nil {
		flex = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 2, 0, false).
			AddItem(tablePane, 0, 3, true).  // Table: 3 parts
			AddItem(portPane, 0, 3, false).  // Port: 3 parts
			AddItem(graphView, 0, 3, false). // Graph: 3 parts
			AddItem(errorView, 0, 2, false). // Logs: 2 parts
			AddItem(footer, 2, 0, false)
	}

	flex.SetBackgroundColor(tcell.ColorBlack)

	err := app.SetRoot(flex, true).Run()
	closeAppStop() // fallback: ensure goroutine stops even on non-interactive exit
	return err
}
