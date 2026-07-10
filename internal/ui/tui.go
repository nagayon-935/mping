package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

const (
	tableMaxRows  = 10                     // max ping targets shown without scrolling; keeps UI readable at typical terminal heights
	minUIRefresh  = 200 * time.Millisecond // minimum refresh to avoid flicker at very short ping intervals
	fastUIRefresh = 100 * time.Millisecond // UI refresh rate when ping interval < minUIRefresh
)

var newApplication = tview.NewApplication

// RunOptions contains all parameters for the Run function.
type RunOptions struct {
	Targets      []*stats.TargetStats
	Interval     time.Duration
	Timeout      time.Duration
	DoneCh       chan struct{} // closed when pinger finishes (count-limited mode); nil means unlimited
	SourceIPv4   string
	SourceIPv6   string
	PacketSize   int
	InitialLogs  []string
	TraceEnabled bool
	MTREnabled   bool
	PortEnabled  bool
	HTTPEnabled  bool
	ASNEnabled   bool
	// HTTPResults returns live HTTP health-check results for the HTTP Monitor
	// pane. Called on every render tick (rather than once at startup) so it
	// reflects a checker swapped in by OnResetHTTP after Run has started.
	// Nil when HTTPEnabled is false.
	HTTPResults func() []*stats.HTTPCheckResult
	// Thresholds overrides the colour-coding / alert boundaries. Nil keeps the
	// built-in defaults.
	Thresholds *Thresholds
	// ExternalCloseCh, when closed, causes the TUI to display a reload message
	// and stop. Nil is safe: a nil receive channel blocks forever in select,
	// effectively disabling the case (normal mode).
	ExternalCloseCh <-chan struct{}
	// ExternalLogCh delivers messages to the Log pane from outside the TUI.
	// Each received string is appended as-is (tview colour tags are supported).
	// Nil is safe: a nil receive channel blocks forever in select, disabling
	// the case.
	ExternalLogCh <-chan string
	OnStop        func()
	OnRestart     func() error
	OnResetTrace  func()
	OnResetMTR    func()
	OnResetPort   func()
	OnResetHTTP   func()
	// OnAddHost is called when the user adds a host via the 'a' key dialog.
	// A non-nil error is displayed in the Log pane; nil triggers a reload.
	OnAddHost func(host string) error
	// OnDeleteHost is called when the user deletes a host via the 'd' key dialog.
	// A non-nil error is displayed in the Log pane; nil triggers a reload.
	OnDeleteHost func(host string) error
	// Groups defines named groups of targets for grouped display.
	// Nil means flat (ungrouped) layout — existing behaviour.
	Groups []TargetGroup
}

// Run starts the TUI application with the given options.
func Run(opts RunOptions) error {
	if opts.Thresholds != nil {
		setActiveThresholds(*opts.Thresholds)
	}
	targets := opts.Targets
	interval := opts.Interval
	doneCh := opts.DoneCh
	sourceIPv4 := opts.SourceIPv4
	sourceIPv6 := opts.SourceIPv6
	packetSize := opts.PacketSize
	initialLogs := opts.InitialLogs
	traceEnabled := opts.TraceEnabled
	mtrEnabled := opts.MTREnabled
	portEnabled := opts.PortEnabled
	httpEnabled := opts.HTTPEnabled
	httpResultsFunc := opts.HTTPResults
	asnEnabled := opts.ASNEnabled
	onStop := opts.OnStop
	onRestart := opts.OnRestart
	onResetTrace := opts.OnResetTrace
	onResetMTR := opts.OnResetMTR
	onResetPort := opts.OnResetPort
	onResetHTTP := opts.OnResetHTTP
	onAddHost := opts.OnAddHost
	onDeleteHost := opts.OnDeleteHost
	groups := opts.Groups

	externalCloseCh := opts.ExternalCloseCh
	externalLogCh := opts.ExternalLogCh

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

	dnsEnabled := false
	for _, t := range targets {
		v := t.GetView()
		if v.DNSServer != "" && v.DNSServer != "-" {
			dnsEnabled = true
			break
		}
	}

	// Columns: Src IP, Dst IP, DNS, ASN, Success, Loss, Loss Ratio, RTT, Avg, Jitter, Size, MTU, TTL, Error, Last Loss
	cols := mainTableColumns(dnsEnabled, asnEnabled)
	fullHeaders := make([]string, len(cols))
	fullAligns := make([]int, len(cols))
	baseWidths := make([]int, len(cols))
	minWidths := make([]int, len(cols))
	maxWidths := make([]int, len(cols))
	shrinkPriorities := make([]int, len(cols))
	growPriorities := make([]int, len(cols))
	for i, c := range cols {
		fullHeaders[i] = c.name
		fullAligns[i] = c.align
		baseWidths[i] = c.base
		minWidths[i] = c.min
		maxWidths[i] = c.max
		shrinkPriorities[i] = c.shrinkPriority
		growPriorities[i] = c.growPriority
	}

	// compactCols mirrors buildCompactLayout's fixed Host/Path/Stats/Error
	// schema; only its priorities are needed here since headers/min/max come
	// from buildCompactLayout itself (recomputed every tick from live data).
	compactCols := compactTableColumns()
	compactShrinkPriorities := make([]int, len(compactCols))
	compactGrowPriorities := make([]int, len(compactCols))
	for i, c := range compactCols {
		compactShrinkPriorities[i] = c.shrinkPriority
		compactGrowPriorities[i] = c.growPriority
	}

	// Update error width index: the Error column's max grows to fit the
	// widest known error string rather than staying at its static base.
	errorIdx := columnsByName(cols, "Error")
	baseWidths[errorIdx] = calcInitialTableErrorWidth(targets, fullHeaders[errorIdx], baseWidths[errorIdx])
	maxWidths[errorIdx] = baseWidths[errorIdx]

	headerColor := tcell.ColorYellow
	rowColor := tcell.ColorWhite

	// Recalculate dynamic column widths based on current output text.
	calcColumnWidths := func() []int {
		widths := append([]int(nil), baseWidths...)
		for i, c := range cols {
			if !c.dynamic {
				continue
			}
			maxWidth := runewidth.StringWidth(c.name)
			for _, t := range targets {
				ctx := columnRowContext{view: t.GetView(), sourceIPv4: sourceIPv4, sourceIPv6: sourceIPv6, packetSize: packetSize}
				if w := runewidth.StringWidth(c.render(ctx)); w > maxWidth {
					maxWidth = w
				}
			}
			widths[i] = maxWidth
		}
		return widths
	}

	widths := calcColumnWidths()
	activeHeaders := append([]string(nil), fullHeaders...)
	activeAligns := append([]int(nil), fullAligns...)
	rowCount := len(targets) + 1
	compactLayout := false

	// groupRowMap maps visible table data-row index to its logical row entry.
	// Rebuilt on every updateTable call when groups are active.
	var groupRowMap []groupTableRow

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

	// Error log state (declared before the monitor panes below, whose
	// render closures capture errorLogs/errorView/lastPortStatuses/
	// lastHTTPStatuses directly).
	errorLogs := []string{}
	lastLossTimes := make(map[string]time.Time)
	alertState := make(map[string]alertFlags)
	lastPortStatuses := make(map[string]string)
	lastHTTPStatuses := make(map[string]string)

	for _, line := range initialLogs {
		appendErrorLog(&errorLogs, errorView, line)
	}

	tracePaneObj := newMonitorPane(traceEnabled, " Traceroute Monitor ", func(availW int) string {
		return renderTracerouteTable(targets, availW)
	})
	mtrPaneObj := newMonitorPane(mtrEnabled, " MTR Monitor ", func(availW int) string {
		return renderMTRTable(targets, availW, sourceIPv4, sourceIPv6)
	})
	portPaneObj := newMonitorPane(portEnabled, " Port Monitor ", func(availW int) string {
		return renderPortMonitorTable(targets, availW, lastPortStatuses, &errorLogs, errorView)
	})
	httpPaneObj := newMonitorPane(httpEnabled, " HTTP Monitor ", func(availW int) string {
		var httpResults []*stats.HTTPCheckResult
		if httpResultsFunc != nil {
			httpResults = httpResultsFunc()
		}
		return renderHTTPMonitorTable(httpResults, availW, lastHTTPStatuses, &errorLogs, errorView)
	})
	sidePanes := []*monitorPane{tracePaneObj, mtrPaneObj, portPaneObj, httpPaneObj}

	header := tview.NewTextView().
		SetText(fmt.Sprintf("MPING - Multi Ping Tool | Interval: %dms", interval.Milliseconds())).
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorGreen).
		SetWrap(false)
	header.SetBackgroundColor(tcell.ColorBlack)

	footer := tview.NewTextView().
		SetText("Tab: Focus | a: Add host | d: Del host | q: Quit | s: Stop | R: Reset").
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorYellow).
		SetWrap(false)
	footer.SetBackgroundColor(tcell.ColorBlack)

	// Add host input (shown in footer row)
	addHostInput := tview.NewInputField().
		SetLabel(" Add host: ").
		SetFieldBackgroundColor(tcell.ColorBlack).
		SetFieldTextColor(tcell.ColorWhite).
		SetLabelColor(tcell.ColorYellow)

	// Delete host input (shown in footer row, same pattern as addHostInput)
	deleteHostInput := tview.NewInputField().
		SetLabel(" Delete host: ").
		SetFieldBackgroundColor(tcell.ColorBlack).
		SetFieldTextColor(tcell.ColorWhite).
		SetLabelColor(tcell.ColorRed)

	pages := tview.NewPages().
		AddPage("footer", footer, true, true).
		AddPage("addHost", addHostInput, true, false).
		AddPage("deleteHost", deleteHostInput, true, false)

	updateTickerCh := make(chan time.Duration, 1)
	var updateTable func()

	addHostInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			host := strings.TrimSpace(addHostInput.GetText())
			addHostInput.SetText("")
			if host != "" && onAddHost != nil {
				go func() {
					if err := onAddHost(host); err != nil {
						app.QueueUpdateDraw(func() {
							appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[red][%s] Add host error: %v[-]",
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
							appendErrorLog(&errorLogs, errorView, fmt.Sprintf("[red][%s] Delete host error: %v[-]",
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

	updateTable = func() {
		table.Clear()

		tablePane.SetTitle(" Ping Monitor ")

		_, _, availableTableWidth, _ := tablePane.GetInnerRect()
		availableColumnsWidth := availableTableWidth - (len(fullHeaders) + 1)
		if availableColumnsWidth < 0 {
			availableColumnsWidth = 0
		}

		updatedWidths := calcColumnWidthsCached()
		dynamicMaxWidths := append([]int(nil), maxWidths...)
		for i, c := range cols {
			if c.dynamic && updatedWidths[i] > dynamicMaxWidths[i] {
				dynamicMaxWidths[i] = updatedWidths[i]
			}
		}
		fitted, ok := fitWidthsToAvailable(updatedWidths, minWidths, dynamicMaxWidths, shrinkPriorities, growPriorities, availableColumnsWidth)

		lastLossBase := cols[columnsByName(cols, "Last Loss")].base
		compact := buildCompactLayout(targets, packetSize, sourceIPv4, sourceIPv6, lastLossBase)
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
		compactWidths, compactOK := fitWidthsToAvailable(compactDesired, compactMin, compactMax, compactShrinkPriorities, compactGrowPriorities, compactAvailableColumnsWidth)

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

		// When groups are active, override rowCount with the group layout.
		if len(groups) > 0 && !compactLayout {
			groupRowMap = buildGroupRows(targets, groups)
			rowCount = len(groupRowMap) + 1
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

		// Pass 1: check for new errors and alert state for all targets, and
		// cache each target's rendered cell text so pass 2 doesn't
		// re-render every column a second time per target per tick.
		now := time.Now()
		views := make([]stats.TargetView, len(targets))
		var rowCtxCache []columnRowContext
		var textsCache [][]string
		if !compactLayout {
			rowCtxCache = make([]columnRowContext, len(targets))
			textsCache = make([][]string, len(targets))
		}
		for i, t := range targets {
			view := t.GetView()
			views[i] = view
			rowSourceIP := displaySourceIPForDst(view.IP, sourceIPv4, sourceIPv6)
			if !view.LastLossTime.IsZero() {
				lastTime, exists := lastLossTimes[view.Host]
				if !exists || view.LastLossTime.After(lastTime) {
					lastLossTimes[view.Host] = view.LastLossTime
					msg := buildErrorLogMessage(view, rowSourceIP, view.LastError, view.LastLossTime)
					appendErrorLog(&errorLogs, errorView, msg)
				}
			}
			if !compactLayout {
				ctx := columnRowContext{
					view: view, sourceIPv4: sourceIPv4, sourceIPv6: sourceIPv6,
					packetSize: packetSize, lossRate: calcLossRate(view),
				}
				rowCtxCache[i] = ctx
				textsCache[i] = renderRowTexts(cols, ctx)
				state := alertState[view.Host]
				state, msgs := updateAlertState(view, rowSourceIP, ctx.lossRate, now, state)
				for _, msg := range msgs {
					appendErrorLog(&errorLogs, errorView, msg)
				}
				alertState[view.Host] = state
			}
		}

		// Pass 2: render table rows.
		if len(groups) > 0 && !compactLayout {
			// Group-aware rendering: header → [targets] per group.
			for rowIdx, row := range groupRowMap {
				tableRow := rowIdx + 1
				switch row.kind {
				case groupRowHeader:
					memberCount := len(groups[row.groupIdx].Indices)
					setGroupHeaderRow(table, tableRow, len(activeHeaders),
						row.groupName, memberCount)
				case groupRowUngrouped, groupRowTarget:
					cells := renderRowCells(cols, textsCache[row.targetIdx], widths, fullAligns, rowCtxCache[row.targetIdx], rowColor)
					for c, cell := range cells {
						table.SetCell(tableRow, c, cell)
					}
				}
			}
		} else if !compactLayout {
			// Flat rendering (no groups, not compact — compact rows were
			// already rendered earlier via the compactRows loop above).
			for i := range targets {
				row := i + 1
				cells := renderRowCells(cols, textsCache[i], widths, fullAligns, rowCtxCache[i], rowColor)
				for c, cell := range cells {
					table.SetCell(row, c, cell)
				}
			}
		}

		for _, mp := range sidePanes {
			mp.refresh()
		}
	}

	appStop := make(chan struct{})
	var appStopOnce sync.Once
	closeAppStop := func() { appStopOnce.Do(func() { close(appStop) }) }

	// Keys
	app.SetInputCapture(newInputHandler(inputHandlerDeps{
		app:              app,
		table:            table,
		addHostInput:     addHostInput,
		deleteHostInput:  deleteHostInput,
		graphView:        graphView,
		errorView:        errorView,
		footer:           footer,
		sidePanes:        sidePanes,
		pages:            pages,
		targets:          targets,
		rowCount:         &rowCount,
		errorLogs:        &errorLogs,
		lastLossTimes:    &lastLossTimes,
		alertState:       &alertState,
		lastPortStatuses: &lastPortStatuses,
		lastHTTPStatuses: &lastHTTPStatuses,
		traceEnabled:     traceEnabled,
		mtrEnabled:       mtrEnabled,
		portEnabled:      portEnabled,
		httpEnabled:      httpEnabled,
		onStop:           onStop,
		onRestart:        onRestart,
		onResetTrace:     onResetTrace,
		onResetMTR:       onResetMTR,
		onResetPort:      onResetPort,
		onResetHTTP:      onResetHTTP,
		onAddHost:        onAddHost,
		onDeleteHost:     onDeleteHost,
		closeAppStop:     closeAppStop,
	}))
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
			case msg := <-externalLogCh:
				// Deliver external log messages (e.g. watcher validation errors)
				// immediately to the Log pane without waiting for the next tick.
				app.QueueUpdateDraw(func() {
					appendErrorLog(&errorLogs, errorView, msg)
				})
			case <-externalCloseCh:
				// External reload requested (e.g. YAML file changed).
				app.QueueUpdateDraw(func() {
					appendErrorLog(&errorLogs, errorView,
						fmt.Sprintf("[yellow][%s] Reloading configuration...[-]",
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
					updateTable()
				})
				// Stop refreshing since pinger is done
				return
			case <-appStop:
				return
			}
		}
	}()

	// Build layout dynamically: append enabled monitor panes (trace, mtr, port)
	// with weight 3 when alone, 2 when multiple.
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

	err := app.SetRoot(flex, true).Run()
	closeAppStop() // fallback: ensure goroutine stops even on non-interactive exit
	return err
}
