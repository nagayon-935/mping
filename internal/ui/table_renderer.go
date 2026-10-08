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
// the uiSession mailbox); no mutex guards them and background goroutines
// must post updates through that mailbox.
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

	selectionEnabled  bool
	selectedID        uint64
	afterUpdate       func()
	scrollToSelection bool
	beforeUpdate      func()
	vs                *viewState
}

// newTableRenderer builds the column schema (from mainTableColumns, keyed
// off whether any target has a DNS server set) and its derived
// headers/aligns/widths/priorities, seeds the Error column's data-derived
// max width, and primes the initial column widths and log lines.
func newTableRenderer(
	targets []*stats.TargetStats, sourceIPv4, sourceIPv6 string, packetSize int, asnEnabled, ptrEnabled, dscpEnabled bool, groups []TargetGroup,
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

	cols := mainTableColumns(dnsEnabled, asnEnabled, ptrEnabled, dscpEnabled)
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
		vs: vs,
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
//
// Recomputed every tick on purpose. A previous version cached this and only
// invalidated on terminal resize, which pinned the ASN and Dst IP columns to
// whatever they measured on the first tick — before the Cymru lookup and DNS
// resolution had filled them in — so their content stayed truncated until
// the user resized. See BenchmarkCalcColumnWidths for the cost this trades
// against.
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
	if tr.beforeUpdate != nil {
		tr.beforeUpdate()
	}
	tr.table.Clear()
	tr.tablePane.SetTitle(" Ping Monitor ")

	// One GetView() snapshot per target for this whole tick — width calc,
	// compact layout, and row rendering all read from this same slice
	// instead of each calling GetView() independently (P2: GetView() copies
	// the full RTT history ring, so this collapses what used to be 10+
	// redundant calls per target per tick down to exactly one).
	views := fetchViews(tr.targets)
	compactRows := tr.chooseLayout(views)
	if tr.selectionEnabled {
		tr.reconcileSelection()
	}
	// Only the visible (plus margin) row window is rendered — see
	// visibleRowWindow. rowCount stays the full logical count regardless,
	// so input_handler.go's scroll math is unaffected.
	offsetRow, _ := tr.table.GetOffset()

	setHeaderRow(tr.table, 0, tr.activeHeaders, tr.widths, tr.activeAligns, tr.headerColor)
	rowCtx, texts := tr.scanTargets(views)
	if tr.compactLayout {
		tr.renderCompactRows(compactRows, offsetRow)
	} else {
		tr.renderFullRows(rowCtx, texts, offsetRow)
	}

	if tr.selectionEnabled {
		tr.highlightSelection()
	}
	if tr.afterUpdate != nil {
		tr.afterUpdate()
	}
	for _, mp := range tr.sidePanes {
		mp.refresh()
	}
}

// chooseLayout fits the full layout to the pane, falling back to the compact
// one when it does not fit, and sets the active headers, widths and row
// count. When neither fits the previous tick's layout stays. It returns the
// compact rows whenever the full layout did not fit.
func (tr *tableRenderer) chooseLayout(views []stats.TargetView) []compactRow {
	_, _, availableTableWidth, _ := tr.tablePane.GetInnerRect()
	availableColumnsWidth := max(availableTableWidth-(len(tr.fullHeaders)+1), 0)

	updatedWidths := tr.calcColumnWidths(views)
	dynamicMaxWidths := append([]int(nil), tr.maxWidths...)
	for i, c := range tr.cols {
		if c.dynamic && updatedWidths[i] > dynamicMaxWidths[i] {
			dynamicMaxWidths[i] = updatedWidths[i]
		}
	}
	fitted, ok := fitWidthsToAvailable(updatedWidths, tr.minWidths, dynamicMaxWidths, tr.shrinkPriorities, tr.growPriorities, availableColumnsWidth)

	// The compact layout is only a fallback for when the full layout
	// doesn't fit; skip computing it entirely in the common case.
	var compactRows []compactRow
	if ok {
		tr.compactLayout = false
		tr.widths = fitted
		tr.activeHeaders = append([]string(nil), tr.fullHeaders...)
		tr.activeAligns = append([]int(nil), tr.fullAligns...)
		tr.rowCount = len(tr.targets) + 1
	} else {
		compact := buildCompactLayout(views, tr.packetSize, tr.sourceIPv4, tr.sourceIPv6, tr.lastLossBase)
		compactRows = compact.rows
		compactAvailableColumnsWidth := max(availableTableWidth-(len(compact.headers)+1), 0)
		if widths, fits := fitWidthsToAvailable(compact.desired, compact.min, compact.max, tr.compactShrinkPriorities, tr.compactGrowPriorities, compactAvailableColumnsWidth); fits {
			tr.compactLayout = true
			tr.widths = widths
			tr.activeHeaders = append([]string(nil), compact.headers...)
			tr.activeAligns = append([]int(nil), compact.aligns...)
			tr.rowCount = len(compactRows) + 1
		}
	}

	// When groups are active, override rowCount with the group layout.
	if len(tr.groups) > 0 && !tr.compactLayout {
		tr.groupRowMap = buildGroupRows(tr.targets, tr.groups)
		tr.rowCount = len(tr.groupRowMap) + 1
	}
	return compactRows
}

// scanTargets logs new losses and alert transitions for every target and,
// in the full layout, renders each target's cell texts once so rows can
// reuse them (nil slices in the compact layout).
func (tr *tableRenderer) scanTargets(views []stats.TargetView) ([]columnRowContext, [][]string) {
	now := time.Now()
	var rowCtx []columnRowContext
	var texts [][]string
	if !tr.compactLayout {
		rowCtx = make([]columnRowContext, len(tr.targets))
		texts = make([][]string, len(tr.targets))
	}
	for i := range tr.targets {
		view := views[i]
		rowSourceIP := displaySourceIPForDst(view.IP, tr.sourceIPv4, tr.sourceIPv6)
		if !view.LastLossTime.IsZero() {
			lastTime, exists := tr.vs.lastLossTimes[targetViewKey(view)]
			if !exists || view.LastLossTime.After(lastTime) {
				tr.vs.lastLossTimes[targetViewKey(view)] = view.LastLossTime
				tr.vs.appendLog(buildErrorLogMessage(view, rowSourceIP, view.LastError, view.LastLossTime))
			}
		}
		if tr.compactLayout {
			continue
		}
		ctx := columnRowContext{
			view: view, sourceIPv4: tr.sourceIPv4, sourceIPv6: tr.sourceIPv6,
			packetSize: tr.packetSize, lossRate: calcLossRate(view),
		}
		rowCtx[i] = ctx
		texts[i] = renderRowTexts(tr.cols, ctx)
		state, msgs := updateAlertState(view, rowSourceIP, ctx.lossRate, now, tr.vs.alertState[targetViewKey(view)])
		for _, msg := range msgs {
			tr.vs.appendLog(msg)
		}
		tr.vs.alertState[targetViewKey(view)] = state
	}
	return rowCtx, texts
}

func (tr *tableRenderer) renderCompactRows(rows []compactRow, offsetRow int) {
	pick := func(right, left string) string {
		if right != "" {
			return right
		}
		return left
	}
	start, end := visibleRowWindow(offsetRow, len(rows))
	for i := start; i < end; i++ {
		r := rows[i]
		values := []string{pick(r.hostR, r.hostL), pick(r.pathR, r.pathL), pick(r.statR, r.statL), pick(r.errR, r.errL)}
		tr.setRow(i+1, buildCompactRowCells(values, tr.widths, tr.activeAligns, tr.rowColor))
	}
}

// renderFullRows renders the full layout, flat or grouped. Grouped rows are
// windowed by table-row index (tr.groupRowMap), not target index, since
// group header rows don't correspond to a target — a header inside the
// window must render even if its members don't.
func (tr *tableRenderer) renderFullRows(rowCtx []columnRowContext, texts [][]string, offsetRow int) {
	if len(tr.groups) == 0 {
		start, end := visibleRowWindow(offsetRow, len(tr.targets))
		for i := start; i < end; i++ {
			tr.setRow(i+1, renderRowCells(tr.cols, texts[i], tr.widths, tr.fullAligns, rowCtx[i], tr.rowColor))
		}
		return
	}
	start, end := visibleRowWindow(offsetRow, len(tr.groupRowMap))
	for rowIdx := start; rowIdx < end; rowIdx++ {
		row := tr.groupRowMap[rowIdx]
		tableRow := rowIdx + 1
		switch row.kind {
		case groupRowSpacer:
			setGroupSpacerRow(tr.table, tableRow, len(tr.activeHeaders))
		case groupRowHeader:
			setGroupHeaderRow(tr.table, tableRow, len(tr.activeHeaders), row.groupName, len(tr.groups[row.groupIdx].Indices))
		case groupRowSubHeader:
			setHeaderRow(tr.table, tableRow, tr.activeHeaders, tr.widths, tr.activeAligns, tr.headerColor)
		case groupRowUngrouped, groupRowTarget:
			tr.setRow(tableRow, renderRowCells(tr.cols, texts[row.targetIdx], tr.widths, tr.fullAligns, rowCtx[row.targetIdx], tr.rowColor))
		}
	}
}

func (tr *tableRenderer) setRow(row int, cells []*tview.TableCell) {
	for c, cell := range cells {
		tr.table.SetCell(row, c, cell)
	}
}
