package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

const (
	// tableMaxRows is for the main stats table
	tableMaxRows    = 10
	tablePaneHeight = ((tableMaxRows + 1) * 2) + 1 + 2 // table height + pane border

	minUIRefresh  = 200 * time.Millisecond // minimum UI refresh interval when ping interval is very short
	fastUIRefresh = 100 * time.Millisecond // UI refresh rate used when ping interval < minUIRefresh
)

var newApplication = tview.NewApplication

func Run(targets []*stats.TargetStats, interval time.Duration, doneCh chan struct{}, sourceIPv4, sourceIPv6 string, packetSize int, initialLogs []string, traceEnabled bool, portEnabled bool, onStop func(), onRestart func() error) error {
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
		SetWordWrap(true). // Ensure long messages wrap
		SetChangedFunc(func() {
			app.Draw()
		})
	errorView.SetBorder(true).SetTitle(" Log ").SetTitleColor(tcell.ColorRed).SetBorderColor(tcell.ColorRed)
	errorView.SetBackgroundColor(tcell.ColorBlack)

	// Set black background and darkgray borders
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetBorderColor(tcell.ColorWhite)
	table.SetBordersColor(tcell.ColorWhite)


	// Columns: Src IP, Dst IP, Success, Loss, Loss Ratio, RTT, Avg, Jitter, Size, MTU, TTL, Error, Last Loss
	fullHeaders := []string{"Src IP", "Dst IP", "Success", "Loss", "Loss Ratio", "RTT", "Avg", "Jitter", "Size", "MTU", "TTL", "Error", "Last Loss"}
	fullAligns := []int{
		tview.AlignLeft, tview.AlignLeft, tview.AlignRight, tview.AlignRight, tview.AlignRight,
		tview.AlignRight, tview.AlignRight, tview.AlignRight, // RTTs
		tview.AlignRight, tview.AlignRight, tview.AlignRight, tview.AlignLeft, tview.AlignLeft,
	}
	// Src IP / Dst IP are dynamically resized from the rendered content.
	// Error width is fixed at startup to prevent table size jumps when new errors arrive.
	baseWidths := []int{6, 6, 8, 7, 10, 10, 10, 10, 6, 6, 5, 30, 15}
	baseWidths[11] = calcInitialTableErrorWidth(targets, fullHeaders[11], baseWidths[11])
	minWidths := []int{4, 8, 5, 4, 6, 7, 7, 7, 4, 4, 3, 8, 8}
	maxWidths := []int{45, 60, 10, 10, 12, 12, 12, 12, 8, 8, 6, baseWidths[11], 18}

	headerColor := tcell.ColorYellow
	rowColor := tcell.ColorWhite

	// Recalculate dynamic column widths based on current output text.
	calcColumnWidths := func() []int {
		widths := append([]int(nil), baseWidths...)
		for _, c := range []int{0, 1} {
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
		// Override with double-line box drawing characters.
		tracePane.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
			if width < 2 || height < 2 {
				return x + 1, y + 1, width - 2, height - 2
			}
			style := tcell.StyleDefault.Foreground(traceBorderColor)
			screen.SetContent(x, y, '╔', nil, style)
			for i := x + 1; i < x+width-1; i++ {
				screen.SetContent(i, y, '═', nil, style)
			}
			screen.SetContent(x+width-1, y, '╗', nil, style)
			screen.SetContent(x, y+height-1, '╚', nil, style)
			for i := x + 1; i < x+width-1; i++ {
				screen.SetContent(i, y+height-1, '═', nil, style)
			}
			screen.SetContent(x+width-1, y+height-1, '╝', nil, style)
			for i := y + 1; i < y+height-1; i++ {
				screen.SetContent(x, i, '║', nil, style)
				screen.SetContent(x+width-1, i, '║', nil, style)
			}
			tview.Print(screen, " Traceroute Monitor ", x+1, y, width-2, tview.AlignCenter, traceBorderColor)
			return x + 1, y + 1, width - 2, height - 2
		})
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
		portPane.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
			if width < 2 || height < 2 {
				return x + 1, y + 1, width - 2, height - 2
			}
			style := tcell.StyleDefault.Foreground(portBorderColor)
			screen.SetContent(x, y, '╔', nil, style)
			for i := x + 1; i < x+width-1; i++ {
				screen.SetContent(i, y, '═', nil, style)
			}
			screen.SetContent(x+width-1, y, '╗', nil, style)
			screen.SetContent(x, y+height-1, '╚', nil, style)
			for i := x + 1; i < x+width-1; i++ {
				screen.SetContent(i, y+height-1, '═', nil, style)
			}
			screen.SetContent(x+width-1, y+height-1, '╝', nil, style)
			for i := y + 1; i < y+height-1; i++ {
				screen.SetContent(x, i, '║', nil, style)
				screen.SetContent(x+width-1, i, '║', nil, style)
			}
			tview.Print(screen, " Port Monitor ", x+1, y, width-2, tview.AlignCenter, portBorderColor)
			return x + 1, y + 1, width - 2, height - 2
		})
	}

	// Error log state
	errorLogs := []string{}
	lastLossTimes := make(map[string]time.Time)
	alertState := make(map[string]alertFlags)

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

		updatedWidths := calcColumnWidths()
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
			// Compute Host column width from the longest hostname (with 1-space padding each side).
			hostColW := runewidth.StringWidth("Host")
			for _, t := range targets {
				if w := runewidth.StringWidth(t.GetView().Host); w > hostColW {
					hostColW = w
				}
			}
			hostColW += 2 // 1 space left + 1 space right

			// Compute Route column width from the longest route string (with 1-space padding each side).
			routeColW := runewidth.StringWidth("Route")
			for _, t := range targets {
				hops := t.GetView().TraceHops
				if w := runewidth.StringWidth(strings.Join(hops, " -> ")); w > routeColW {
					routeColW = w
				}
			}
			routeColW += 2 // 1 space left + 1 space right

			// Expand Route column to fill available terminal width.
			// Available width = inner rect width - 3 border chars (│host│route│).
			_, _, availW, _ := traceView.GetInnerRect()
			if expanded := availW - hostColW - 3; expanded > routeColW {
				routeColW = expanded
			}

			// cell returns text with a leading space, right-padded to fill colW.
			cell := func(text string, colW int) string {
				return formatCellText(" "+text, colW, tview.AlignLeft)
			}

			h := strings.Repeat("─", hostColW)
			r := strings.Repeat("─", routeColW)

			var sb strings.Builder

			// Top border.
			fmt.Fprintf(&sb, "[white]┌%s┬%s┐[-]\n", h, r)

			// Header row: yellow bold labels, darkgray borders.
			fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
				cell("Host", hostColW), cell("Route", routeColW))

			// Header separator.
			fmt.Fprintf(&sb, "[white]├%s┼%s┤[-]\n", h, r)

			// Data rows: host in cyan, route in white, darkgray borders, separator between rows.
			dataTargets := make([]*stats.TargetStats, 0, len(targets))
			for _, t := range targets {
				if len(t.GetView().TraceHops) > 0 {
					dataTargets = append(dataTargets, t)
				}
			}
			for i, t := range dataTargets {
				view := t.GetView()
				route := strings.Join(view.TraceHops, " -> ")
				fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[-]\n",
					cell(view.Host, hostColW), cell(route, routeColW))
				if i < len(dataTargets)-1 {
					fmt.Fprintf(&sb, "[white]├%s┼%s┤[-]\n", h, r)
				}
			}

			// Bottom border.
			fmt.Fprintf(&sb, "[white]└%s┴%s┘[-]\n", h, r)

			traceView.SetText(sb.String())
		}

		if portEnabled && portView != nil {
			_, _, availW, _ := portView.GetInnerRect()

			// Column widths: Target, Port, Status, Open/Closed, Last Change
			targetColW := runewidth.StringWidth("Target")
			for _, t := range targets {
				if w := runewidth.StringWidth(t.GetView().Host); w > targetColW {
					targetColW = w
				}
			}
			targetColW += 2

			portColW := runewidth.StringWidth("Port")
			for _, t := range targets {
				for _, pr := range t.GetView().PortResults {
					if w := runewidth.StringWidth(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol)); w > portColW {
						portColW = w
					}
				}
			}
			portColW += 2

			serviceColW := runewidth.StringWidth("Service")
			for _, t := range targets {
				for _, pr := range t.GetView().PortResults {
					if w := runewidth.StringWidth(portServiceName(pr.Port, pr.Protocol)); w > serviceColW {
						serviceColW = w
					}
				}
			}
			serviceColW += 2

			statusColW := runewidth.StringWidth("Open|Filtered") + 2
			countColW := runewidth.StringWidth("Open/Closed") + 2
			// Expand Last Change column to fill remaining width
			changeColW := runewidth.StringWidth("Last Change") + 2
			used := targetColW + portColW + serviceColW + statusColW + countColW + changeColW + 6
			if availW > used {
				changeColW += availW - used
			}

			cell := func(text string, colW int) string {
				return formatCellText(" "+text, colW, tview.AlignLeft)
			}
			th := strings.Repeat("─", targetColW)
			ph := strings.Repeat("─", portColW)
			svh := strings.Repeat("─", serviceColW)
			sh := strings.Repeat("─", statusColW)
			ch := strings.Repeat("─", countColW)
			lh := strings.Repeat("─", changeColW)

			var sb strings.Builder
			fmt.Fprintf(&sb, "[white]┌%s┬%s┬%s┬%s┬%s┬%s┐[-]\n", th, ph, svh, sh, ch, lh)
			fmt.Fprintf(&sb, "[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[yellow::b]%s[white]│[-]\n",
				cell("Target", targetColW), cell("Port", portColW), cell("Service", serviceColW),
				cell("Status", statusColW), cell("Open/Closed", countColW), cell("Last Change", changeColW))
			fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┤[-]\n", th, ph, svh, sh, ch, lh)

			// Collect targets that have results for separator logic
			dataTargets := make([]*stats.TargetStats, 0, len(targets))
			for _, t := range targets {
				if len(t.GetView().PortResults) > 0 {
					dataTargets = append(dataTargets, t)
				}
			}

			rowCount := 0
			for ti, t := range dataTargets {
				view := t.GetView()
				for i, pr := range view.PortResults {
					statusColor := "[white]"
					switch pr.Status {
					case "Open":
						statusColor = "[green]"
					case "Closed":
						statusColor = "[red]"
					case "Filtered", "Open|Filtered":
						statusColor = "[yellow]"
					}
					countStr := fmt.Sprintf("%d/%d", pr.OpenCount, pr.ClosedCount)
					changeStr := "-"
					if !pr.LastChange.IsZero() {
						changeStr = formatLossAgo(pr.LastChange)
					}
					targetName := ""
					if i == 0 {
						targetName = view.Host
					}
					fmt.Fprintf(&sb, "[white]│[white]%s[white]│[white]%s[white]│[white]%s[white]│%s%s[-][white]│[white]%s[white]│[white]%s[white]│[-]\n",
						cell(targetName, targetColW),
						cell(fmt.Sprintf("%d/%s", pr.Port, pr.Protocol), portColW),
						cell(portServiceName(pr.Port, pr.Protocol), serviceColW),
						statusColor, cell(pr.Status, statusColW),
						cell(countStr, countColW),
						cell(changeStr, changeColW))
					rowCount++
				}
				if ti < len(dataTargets)-1 {
					fmt.Fprintf(&sb, "[white]├%s┼%s┼%s┼%s┼%s┼%s┤[-]\n", th, ph, svh, sh, ch, lh)
				}
			}
			if rowCount == 0 {
				total := targetColW + portColW + serviceColW + statusColW + countColW + changeColW + 5
				fmt.Fprintf(&sb, "[white]│[darkgray]%s[white]│[-]\n",
					formatCellText(" Waiting for results...", total, tview.AlignLeft))
			}
			fmt.Fprintf(&sb, "[white]└%s┴%s┴%s┴%s┴%s┴%s┘[-]\n", th, ph, svh, sh, ch, lh)

			portView.SetText(sb.String())
		}
	}

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

	return app.SetRoot(flex, true).Run()
}
