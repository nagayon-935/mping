package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// tableRenderer owns the Ping Monitor table's column schema, per-tick width
// calculation, and row rendering — the state and logic that used to live as
// ~15 local variables plus the ~160-line updateTable closure inside Run().
//
// errorLogs/lastLossTimes/alertState/lastPortStatuses/lastHTTPStatuses are
// pointers to state Run() also shares with the key handler
// (inputHandlerDeps), not state tableRenderer owns outright: both sides must
// observe the same reassignment (e.g. the 'R' key resetting errorLogs to a
// fresh slice).
//
// TD-23③: this is Run()'s "tableRenderer type" — update() replaces the
// updateTable closure.
type tableRenderer struct {
	targets    []*stats.TargetStats
	sourceIPv4 string
	sourceIPv6 string
	packetSize int
	groups     []TargetGroup

	cols                    []column
	fullHeaders             []string
	fullAligns              []int
	baseWidths              []int
	minWidths               []int
	maxWidths               []int
	shrinkPriorities        []int
	growPriorities          []int
	compactShrinkPriorities []int
	compactGrowPriorities   []int
	lastLossBase            int

	headerColor tcell.Color
	rowColor    tcell.Color

	table     *tview.Table
	tablePane *tview.Flex
	errorView *tview.TextView
	sidePanes []*monitorPane

	// Mutable render state, recomputed every tick.
	widths        []int
	activeHeaders []string
	activeAligns  []int
	rowCount      int
	compactLayout bool
	groupRowMap   []groupTableRow
	cachedWidths  []int
	lastTermWidth int

	errorLogs        *[]string
	lastLossTimes    *map[string]time.Time
	alertState       *map[string]alertFlags
	lastPortStatuses *map[string]string
	lastHTTPStatuses *map[string]string
}

// newTableRenderer builds the column schema (from mainTableColumns, keyed
// off whether any target has a DNS server set) and its derived
// headers/aligns/widths/priorities, seeds the Error column's data-derived
// max width, and primes the initial column widths and log lines.
func newTableRenderer(
	targets []*stats.TargetStats, sourceIPv4, sourceIPv6 string, packetSize int, asnEnabled bool, groups []TargetGroup,
	table *tview.Table, tablePane *tview.Flex, errorView *tview.TextView, initialLogs []string,
	errorLogs *[]string, lastLossTimes *map[string]time.Time, alertState *map[string]alertFlags,
	lastPortStatuses *map[string]string, lastHTTPStatuses *map[string]string,
) *tableRenderer {
	dnsEnabled := false
	for _, t := range targets {
		v := t.GetView()
		if v.DNSServer != "" && v.DNSServer != "-" {
			dnsEnabled = true
			break
		}
	}

	cols := mainTableColumns(dnsEnabled, asnEnabled)
	tr := &tableRenderer{
		targets: targets, sourceIPv4: sourceIPv4, sourceIPv6: sourceIPv6, packetSize: packetSize, groups: groups,
		cols:             cols,
		fullHeaders:      make([]string, len(cols)),
		fullAligns:       make([]int, len(cols)),
		baseWidths:       make([]int, len(cols)),
		minWidths:        make([]int, len(cols)),
		maxWidths:        make([]int, len(cols)),
		shrinkPriorities: make([]int, len(cols)),
		growPriorities:   make([]int, len(cols)),
		headerColor:      tcell.ColorYellow,
		rowColor:         tcell.ColorWhite,
		table:            table, tablePane: tablePane, errorView: errorView,
		lastTermWidth:    -1,
		errorLogs:        errorLogs,
		lastLossTimes:    lastLossTimes,
		alertState:       alertState,
		lastPortStatuses: lastPortStatuses,
		lastHTTPStatuses: lastHTTPStatuses,
	}
	for i, c := range cols {
		tr.fullHeaders[i] = c.name
		tr.fullAligns[i] = c.align
		tr.baseWidths[i] = c.base
		tr.minWidths[i] = c.min
		tr.maxWidths[i] = c.max
		tr.shrinkPriorities[i] = c.shrinkPriority
		tr.growPriorities[i] = c.growPriority
	}

	// compactCols mirrors buildCompactLayout's fixed Host/Path/Stats/Error
	// schema; only its priorities are needed here since headers/min/max come
	// from buildCompactLayout itself (recomputed every tick from live data).
	compactCols := compactTableColumns()
	tr.compactShrinkPriorities = make([]int, len(compactCols))
	tr.compactGrowPriorities = make([]int, len(compactCols))
	for i, c := range compactCols {
		tr.compactShrinkPriorities[i] = c.shrinkPriority
		tr.compactGrowPriorities[i] = c.growPriority
	}

	// The Error column's max grows to fit the widest known error string
	// rather than staying at its static base.
	errorIdx := columnsByName(cols, "Error")
	tr.baseWidths[errorIdx] = calcInitialTableErrorWidth(targets, tr.fullHeaders[errorIdx], tr.baseWidths[errorIdx])
	tr.maxWidths[errorIdx] = tr.baseWidths[errorIdx]
	tr.lastLossBase = cols[columnsByName(cols, "Last Loss")].base

	tr.widths = tr.calcColumnWidths()
	tr.activeHeaders = append([]string(nil), tr.fullHeaders...)
	tr.activeAligns = append([]int(nil), tr.fullAligns...)
	tr.rowCount = len(targets) + 1

	for _, line := range initialLogs {
		appendErrorLog(tr.errorLogs, errorView, line)
	}

	return tr
}

// calcColumnWidths recalculates dynamic column widths based on current
// output text (Src/Dst IP, DNS, ASN).
func (tr *tableRenderer) calcColumnWidths() []int {
	widths := append([]int(nil), tr.baseWidths...)
	for i, c := range tr.cols {
		if !c.dynamic {
			continue
		}
		maxWidth := runewidth.StringWidth(c.name)
		for _, t := range tr.targets {
			ctx := columnRowContext{view: t.GetView(), sourceIPv4: tr.sourceIPv4, sourceIPv6: tr.sourceIPv6, packetSize: tr.packetSize}
			if w := runewidth.StringWidth(c.render(ctx)); w > maxWidth {
				maxWidth = w
			}
		}
		widths[i] = maxWidth
	}
	return widths
}

// calcColumnWidthsCached recalculates only when the terminal width changes.
func (tr *tableRenderer) calcColumnWidthsCached() []int {
	_, _, curTermWidth, _ := tr.tablePane.GetInnerRect()
	if tr.cachedWidths == nil || curTermWidth != tr.lastTermWidth {
		tr.cachedWidths = tr.calcColumnWidths()
		tr.lastTermWidth = curTermWidth
	}
	return tr.cachedWidths
}

// update re-renders the Ping Monitor table (full or compact layout, flat or
// grouped) and every side pane, for one refresh tick.
func (tr *tableRenderer) update() {
	tr.table.Clear()
	tr.tablePane.SetTitle(" Ping Monitor ")

	_, _, availableTableWidth, _ := tr.tablePane.GetInnerRect()
	availableColumnsWidth := availableTableWidth - (len(tr.fullHeaders) + 1)
	if availableColumnsWidth < 0 {
		availableColumnsWidth = 0
	}

	updatedWidths := tr.calcColumnWidthsCached()
	dynamicMaxWidths := append([]int(nil), tr.maxWidths...)
	for i, c := range tr.cols {
		if c.dynamic && updatedWidths[i] > dynamicMaxWidths[i] {
			dynamicMaxWidths[i] = updatedWidths[i]
		}
	}
	fitted, ok := fitWidthsToAvailable(updatedWidths, tr.minWidths, dynamicMaxWidths, tr.shrinkPriorities, tr.growPriorities, availableColumnsWidth)

	compact := buildCompactLayout(tr.targets, tr.packetSize, tr.sourceIPv4, tr.sourceIPv6, tr.lastLossBase)
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
	compactWidths, compactOK := fitWidthsToAvailable(compactDesired, compactMin, compactMax, tr.compactShrinkPriorities, tr.compactGrowPriorities, compactAvailableColumnsWidth)

	if ok {
		tr.compactLayout = false
		tr.widths = fitted
		tr.activeHeaders = append([]string(nil), tr.fullHeaders...)
		tr.activeAligns = append([]int(nil), tr.fullAligns...)
		tr.rowCount = len(tr.targets) + 1
	} else if compactOK {
		tr.compactLayout = true
		tr.widths = compactWidths
		tr.activeHeaders = append([]string(nil), compactHeaders...)
		tr.activeAligns = append([]int(nil), compactAligns...)
		tr.rowCount = len(compactRows) + 1
	}

	// When groups are active, override rowCount with the group layout.
	if len(tr.groups) > 0 && !tr.compactLayout {
		tr.groupRowMap = buildGroupRows(tr.targets, tr.groups)
		tr.rowCount = len(tr.groupRowMap) + 1
	}

	// Header
	for i, h := range tr.activeHeaders {
		text := formatCellText(h, tr.widths[i], tr.activeAligns[i])
		cell := tview.NewTableCell(text).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(tr.headerColor).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false).
			SetAlign(tr.activeAligns[i])
		if i == len(tr.activeHeaders)-1 {
			cell.SetExpansion(1)
		}
		tr.table.SetCell(0, i, cell)
	}

	pickCompact := func(right, left string) string {
		if right != "" {
			return right
		}
		return left
	}
	if tr.compactLayout {
		for i, r := range compactRows {
			row := i + 1
			values := []string{
				pickCompact(r.hostR, r.hostL),
				pickCompact(r.pathR, r.pathL),
				pickCompact(r.statR, r.statL),
				pickCompact(r.errR, r.errL),
			}
			cells := buildCompactRowCells(values, tr.widths, tr.activeAligns, tr.rowColor)
			for c, cell := range cells {
				tr.table.SetCell(row, c, cell)
			}
		}
	}

	// Pass 1: check for new errors and alert state for all targets, and
	// cache each target's rendered cell text so pass 2 doesn't re-render
	// every column a second time per target per tick.
	now := time.Now()
	views := make([]stats.TargetView, len(tr.targets))
	var rowCtxCache []columnRowContext
	var textsCache [][]string
	if !tr.compactLayout {
		rowCtxCache = make([]columnRowContext, len(tr.targets))
		textsCache = make([][]string, len(tr.targets))
	}
	for i, t := range tr.targets {
		view := t.GetView()
		views[i] = view
		rowSourceIP := displaySourceIPForDst(view.IP, tr.sourceIPv4, tr.sourceIPv6)
		if !view.LastLossTime.IsZero() {
			lastTime, exists := (*tr.lastLossTimes)[view.Host]
			if !exists || view.LastLossTime.After(lastTime) {
				(*tr.lastLossTimes)[view.Host] = view.LastLossTime
				msg := buildErrorLogMessage(view, rowSourceIP, view.LastError, view.LastLossTime)
				appendErrorLog(tr.errorLogs, tr.errorView, msg)
			}
		}
		if !tr.compactLayout {
			ctx := columnRowContext{
				view: view, sourceIPv4: tr.sourceIPv4, sourceIPv6: tr.sourceIPv6,
				packetSize: tr.packetSize, lossRate: calcLossRate(view),
			}
			rowCtxCache[i] = ctx
			textsCache[i] = renderRowTexts(tr.cols, ctx)
			state := (*tr.alertState)[view.Host]
			state, msgs := updateAlertState(view, rowSourceIP, ctx.lossRate, now, state)
			for _, msg := range msgs {
				appendErrorLog(tr.errorLogs, tr.errorView, msg)
			}
			(*tr.alertState)[view.Host] = state
		}
	}

	// Pass 2: render table rows.
	if len(tr.groups) > 0 && !tr.compactLayout {
		// Group-aware rendering: header → [targets] per group.
		for rowIdx, row := range tr.groupRowMap {
			tableRow := rowIdx + 1
			switch row.kind {
			case groupRowHeader:
				memberCount := len(tr.groups[row.groupIdx].Indices)
				setGroupHeaderRow(tr.table, tableRow, len(tr.activeHeaders),
					row.groupName, memberCount)
			case groupRowUngrouped, groupRowTarget:
				cells := renderRowCells(tr.cols, textsCache[row.targetIdx], tr.widths, tr.fullAligns, rowCtxCache[row.targetIdx], tr.rowColor)
				for c, cell := range cells {
					tr.table.SetCell(tableRow, c, cell)
				}
			}
		}
	} else if !tr.compactLayout {
		// Flat rendering (no groups, not compact — compact rows were
		// already rendered earlier via the compactRows loop above).
		for i := range tr.targets {
			row := i + 1
			cells := renderRowCells(tr.cols, textsCache[i], tr.widths, tr.fullAligns, rowCtxCache[i], tr.rowColor)
			for c, cell := range cells {
				tr.table.SetCell(row, c, cell)
			}
		}
	}

	for _, mp := range tr.sidePanes {
		mp.refresh()
	}
}
