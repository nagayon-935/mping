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
// vs is state Run() also shares with the key handler (inputHandlerDeps) and
// the monitor pane render closures, not state tableRenderer owns outright:
// all sides must observe the same reassignment (e.g. the 'R' key resetting
// errorLogs to a fresh slice) — see viewState (TD-51).
//
// TD-23③: this is Run()'s "tableRenderer type" — update() replaces the
// updateTable closure.
//
// Concurrency invariant: like viewState, every field below is mutated only
// from tview's single draw/event-loop goroutine (update() runs inside
// QueueUpdateDraw); no mutex guards them and none may be added from another
// goroutine without first hopping through QueueUpdateDraw.
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

	vs *viewState
}

// newTableRenderer builds the column schema (from mainTableColumns, keyed
// off whether any target has a DNS server set) and its derived
// headers/aligns/widths/priorities, seeds the Error column's data-derived
// max width, and primes the initial column widths and log lines.
func newTableRenderer(
	targets []*stats.TargetStats, sourceIPv4, sourceIPv6 string, packetSize int, asnEnabled bool, groups []TargetGroup,
	table *tview.Table, tablePane *tview.Flex, initialLogs []string, vs *viewState,
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
		table:            table, tablePane: tablePane,
		lastTermWidth: -1,
		vs:            vs,
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

	tr.widths = tr.calcColumnWidths(fetchViews(targets))
	tr.activeHeaders = append([]string(nil), tr.fullHeaders...)
	tr.activeAligns = append([]int(nil), tr.fullAligns...)
	tr.rowCount = len(targets) + 1

	for _, line := range initialLogs {
		tr.vs.appendLog(line)
	}

	return tr
}

// calcColumnWidths recalculates dynamic column widths based on current
// output text (Src/Dst IP, DNS, ASN). views must be the same length as
// tr.targets, in the same order (one GetView() snapshot per target, taken
// once per tick by the caller rather than re-fetched here).
func (tr *tableRenderer) calcColumnWidths(views []stats.TargetView) []int {
	widths := append([]int(nil), tr.baseWidths...)
	for i, c := range tr.cols {
		if !c.dynamic {
			continue
		}
		maxWidth := runewidth.StringWidth(c.name)
		for _, view := range views {
			ctx := columnRowContext{view: view, sourceIPv4: tr.sourceIPv4, sourceIPv6: tr.sourceIPv6, packetSize: tr.packetSize}
			if w := runewidth.StringWidth(c.render(ctx)); w > maxWidth {
				maxWidth = w
			}
		}
		widths[i] = maxWidth
	}
	return widths
}

// calcColumnWidthsCached recalculates only when the terminal width changes.
func (tr *tableRenderer) calcColumnWidthsCached(views []stats.TargetView) []int {
	_, _, curTermWidth, _ := tr.tablePane.GetInnerRect()
	if tr.cachedWidths == nil || curTermWidth != tr.lastTermWidth {
		tr.cachedWidths = tr.calcColumnWidths(views)
		tr.lastTermWidth = curTermWidth
	}
	return tr.cachedWidths
}

// rowRenderMargin extends the rendered row window beyond what's strictly
// visible on each side, so a single scroll step (input_handler.go's
// Up/Down/PgUp/PgDn) lands within an already-rendered range even before
// its forced synchronous update() call (see inputHandlerDeps.forceUpdate)
// completes — a cheap extra safety net, not the primary mechanism.
const rowRenderMargin = 5

// visibleRowWindow returns the [start, end) range of data-row indices
// (0-based — matching indices into tr.targets, tr.groupRowMap, or
// compactRows depending on layout) that should actually be rendered this
// tick, given the table's current scroll offset. offsetRow is the table's
// GetOffset() row (0-based, counting from the first data row below the
// fixed header). totalDataRows is the logical total row count for whichever
// layout is active — tr.rowCount minus 1, i.e. NOT reduced by windowing,
// so input_handler.go's maxOffset scroll math (which reads tr.rowCount)
// stays correct regardless of how few rows are actually rendered.
func visibleRowWindow(offsetRow, totalDataRows int) (start, end int) {
	start = offsetRow - rowRenderMargin
	if start < 0 {
		start = 0
	}
	end = offsetRow + tableMaxRows + 1 + rowRenderMargin
	if end > totalDataRows {
		end = totalDataRows
	}
	return start, end
}

// fetchViews takes one GetView() snapshot per target, in order. Called once
// per tick (or once at construction) so every consumer within that tick —
// width calculation, compact layout, row rendering — shares the same
// snapshot instead of each re-fetching it independently.
func fetchViews(targets []*stats.TargetStats) []stats.TargetView {
	views := make([]stats.TargetView, len(targets))
	for i, t := range targets {
		views[i] = t.GetView()
	}
	return views
}

// update re-renders the Ping Monitor table (full or compact layout, flat or
// grouped) and every side pane, for one refresh tick.
func (tr *tableRenderer) update() {
	tr.table.Clear()
	tr.tablePane.SetTitle(" Ping Monitor ")

	// The current scroll offset, used below to render only the visible
	// (plus margin) row window instead of every row regardless of scroll
	// position — see visibleRowWindow.
	offsetRow, _ := tr.table.GetOffset()

	// One GetView() snapshot per target for this whole tick — width calc,
	// compact layout, and row rendering below all read from this same
	// slice instead of each calling GetView() independently (P2: GetView()
	// copies the full RTT history ring, so this collapses what used to be
	// 10+ redundant calls per target per tick down to exactly one).
	views := fetchViews(tr.targets)

	_, _, availableTableWidth, _ := tr.tablePane.GetInnerRect()
	availableColumnsWidth := availableTableWidth - (len(tr.fullHeaders) + 1)
	if availableColumnsWidth < 0 {
		availableColumnsWidth = 0
	}

	updatedWidths := tr.calcColumnWidthsCached(views)
	dynamicMaxWidths := append([]int(nil), tr.maxWidths...)
	for i, c := range tr.cols {
		if c.dynamic && updatedWidths[i] > dynamicMaxWidths[i] {
			dynamicMaxWidths[i] = updatedWidths[i]
		}
	}
	fitted, ok := fitWidthsToAvailable(updatedWidths, tr.minWidths, dynamicMaxWidths, tr.shrinkPriorities, tr.growPriorities, availableColumnsWidth)

	// The compact layout is only a fallback for when the full layout
	// doesn't fit; skip computing it entirely in the common case (ok ==
	// true) rather than computing and discarding it every tick.
	var compactRows []compactRow
	var compactHeaders []string
	var compactAligns []int
	var compactWidths []int
	compactOK := false
	if !ok {
		compact := buildCompactLayout(views, tr.packetSize, tr.sourceIPv4, tr.sourceIPv6, tr.lastLossBase)
		compactRows = compact.rows
		compactHeaders = compact.headers
		compactAligns = compact.aligns
		compactAvailableColumnsWidth := availableTableWidth - (len(compactHeaders) + 1)
		if compactAvailableColumnsWidth < 0 {
			compactAvailableColumnsWidth = 0
		}
		compactWidths, compactOK = fitWidthsToAvailable(compact.desired, compact.min, compact.max, tr.compactShrinkPriorities, tr.compactGrowPriorities, compactAvailableColumnsWidth)
	}

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
	setHeaderRow(tr.table, 0, tr.activeHeaders, tr.widths, tr.activeAligns, tr.headerColor)

	pickCompact := func(right, left string) string {
		if right != "" {
			return right
		}
		return left
	}
	if tr.compactLayout {
		start, end := visibleRowWindow(offsetRow, len(compactRows))
		for i := start; i < end; i++ {
			r := compactRows[i]
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
	// every column a second time per target per tick. Reuses the views
	// slice fetched once at the top of update() rather than re-fetching.
	now := time.Now()
	var rowCtxCache []columnRowContext
	var textsCache [][]string
	if !tr.compactLayout {
		rowCtxCache = make([]columnRowContext, len(tr.targets))
		textsCache = make([][]string, len(tr.targets))
	}
	for i := range tr.targets {
		view := views[i]
		rowSourceIP := displaySourceIPForDst(view.IP, tr.sourceIPv4, tr.sourceIPv6)
		if !view.LastLossTime.IsZero() {
			lastTime, exists := tr.vs.lastLossTimes[view.Host]
			if !exists || view.LastLossTime.After(lastTime) {
				tr.vs.lastLossTimes[view.Host] = view.LastLossTime
				msg := buildErrorLogMessage(view, rowSourceIP, view.LastError, view.LastLossTime)
				tr.vs.appendLog(msg)
			}
		}
		if !tr.compactLayout {
			ctx := columnRowContext{
				view: view, sourceIPv4: tr.sourceIPv4, sourceIPv6: tr.sourceIPv6,
				packetSize: tr.packetSize, lossRate: calcLossRate(view),
			}
			rowCtxCache[i] = ctx
			textsCache[i] = renderRowTexts(tr.cols, ctx)
			state := tr.vs.alertState[view.Host]
			state, msgs := updateAlertState(view, rowSourceIP, ctx.lossRate, now, state)
			for _, msg := range msgs {
				tr.vs.appendLog(msg)
			}
			tr.vs.alertState[view.Host] = state
		}
	}

	// Pass 2: render table rows. Only the visible (plus margin) row window
	// is actually SetCell'd — rowCount above stays the full logical count
	// regardless, so input_handler.go's scroll math is unaffected by how
	// few rows this pass renders.
	if len(tr.groups) > 0 && !tr.compactLayout {
		// Group-aware rendering: header → [targets] per group. Windowed by
		// table-row index (tr.groupRowMap), not target index, since group
		// header rows don't correspond to a target — a header that falls
		// inside the window must still render even if all its members
		// don't.
		start, end := visibleRowWindow(offsetRow, len(tr.groupRowMap))
		for rowIdx := start; rowIdx < end; rowIdx++ {
			row := tr.groupRowMap[rowIdx]
			tableRow := rowIdx + 1
			switch row.kind {
			case groupRowSpacer:
				setGroupSpacerRow(tr.table, tableRow, len(tr.activeHeaders))
			case groupRowHeader:
				memberCount := len(tr.groups[row.groupIdx].Indices)
				setGroupHeaderRow(tr.table, tableRow, len(tr.activeHeaders),
					row.groupName, memberCount)
			case groupRowSubHeader:
				setHeaderRow(tr.table, tableRow, tr.activeHeaders, tr.widths, tr.activeAligns, tr.headerColor)
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
		start, end := visibleRowWindow(offsetRow, len(tr.targets))
		for i := start; i < end; i++ {
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
