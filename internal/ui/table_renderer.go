package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// tableRenderer owns the Ping Monitor table's column schema, per-tick width
// calculation, and row rendering; Run() calls update() once per tick.
//
// vs is state Run() also shares with the key handler (inputHandlerDeps) and
// the monitor pane render closures, not state tableRenderer owns outright:
// all sides must observe the same reassignment (e.g. the 'R' key resetting
// errorLogs to a fresh slice) — see viewState.
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

	cols             []column
	fullHeaders      []string
	fullAligns       []int
	baseWidths       []int
	minWidths        []int
	maxWidths        []int
	shrinkPriorities []int
	growPriorities   []int

	headerColor tcell.Color
	rowColor    tcell.Color

	table     *tview.Table
	tablePane *tview.Flex
	sidePanes []*monitorPane

	// Mutable render state, recomputed every tick. active holds the indices
	// into cols shown in the full layout (activeCols the columns themselves);
	// widths, activeHeaders and activeAligns are parallel to it. hidden names
	// the columns dropped for width, in the order they were dropped.
	active     []int
	activeCols []column
	hidden     []string
	// widthSeen is each column's widest content so far and everShown marks
	// hideWhenEmpty columns that have had a value: planColumns uses them so
	// a value shrinking back (9.8ms after 10.3ms) or an error clearing does
	// not make columns appear and disappear from one tick to the next.
	widthSeen     []int
	everShown     []bool
	widths        []int
	activeHeaders []string
	activeAligns  []int
	rowCount      int
	groupRowMap   []groupTableRow

	selectionEnabled  bool
	selectedID        uint64
	afterUpdate       func()
	scrollToSelection bool
	beforeUpdate      func()
	lastRenderWidth   int
	lastRenderHeight  int
	vs                *viewState
}

// newTableRenderer builds the column schema (from mainTableColumns, keyed
// off whether any target has a DNS server set) and its derived
// headers/aligns/widths/priorities, seeds the Error column's data-derived
// max width, and primes the initial column widths and log lines.
func newTableRenderer(
	targets []*stats.TargetStats, sourceIPv4, sourceIPv6 string, packetSize int, colOpts columnOptions, groups []TargetGroup,
	table *tview.Table, tablePane *tview.Flex, initialLogs []string, vs *viewState,
) *tableRenderer {
	colOpts.DNS = false
	for _, t := range targets {
		v := t.GetView()
		if v.DNSServer != "" && v.DNSServer != "-" {
			colOpts.DNS = true
			break
		}
	}

	cols := mainTableColumns(colOpts)
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

	// The Error column's max grows to fit the widest known error string
	// rather than staying at its static base.
	errorIdx := columnIndex(cols, "ERR")
	tr.baseWidths[errorIdx] = calcInitialTableErrorWidth(targets, tr.fullHeaders[errorIdx], tr.baseWidths[errorIdx])
	tr.maxWidths[errorIdx] = tr.baseWidths[errorIdx]

	tr.widthSeen = make([]int, len(cols))
	tr.everShown = make([]bool, len(cols))
	tr.widths = tr.calcColumnWidths(fetchViews(targets))
	tr.active = make([]int, len(cols))
	for i := range cols {
		tr.active[i] = i
	}
	tr.activeCols = append([]column(nil), cols...)
	tr.activeHeaders = append([]string(nil), tr.fullHeaders...)
	tr.activeAligns = append([]int(nil), tr.fullAligns...)
	tr.rowCount = len(targets) + 1

	for _, line := range initialLogs {
		tr.vs.appendLog(line)
	}

	return tr
}

// calcColumnWidths computes the initial column widths from the dynamic
// columns' current text (SRC/DST, DNS, ASN, PTR), before the first tick
// plans the layout; every tick after that sizes columns in planColumns.
// views must be the same length as tr.targets, in the same order.
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
// visible on each side, so a small offset change between two update() calls
// (a selection move re-renders through moveSelection→update, but the table
// can also scroll on its own) never exposes unrendered rows — a cheap extra
// safety net, not the primary mechanism.
const rowRenderMargin = 5

// tableInnerHeight returns the inner height available to the Ping Monitor
// table, consulting tablePane when the inner table primitive has not yet been
// laid out by Flex.Draw.
func (tr *tableRenderer) tableInnerHeight() int {
	_, _, _, h := tr.table.GetInnerRect()
	if _, _, _, paneH := tr.tablePane.GetInnerRect(); paneH > h {
		h = paneH
	}
	return h
}

// visibleDataRows returns the number of data rows that fit in the table's
// current inner height, floored at tableMinVisibleRows when the widget has not been
// sized past the default minimum yet.
func (tr *tableRenderer) visibleDataRows() int {
	return max(tableMinVisibleRows, (tr.tableInnerHeight()-2)/2)
}

// visibleRowWindow returns the [start, end) range of data-row indices
// (0-based — matching indices into tr.targets or tr.groupRowMap, depending
// on whether groups are shown) that should actually be rendered this
// tick, given the table's current scroll offset. offsetRow is the table's
// GetOffset() row (0-based, counting from the first data row below the
// fixed header). totalDataRows is the logical total row count for whichever
// layout is active — tr.rowCount minus 1, i.e. NOT reduced by windowing,
// so reconcileSelection's offset clamping (which reads tr.rowCount) stays
// correct regardless of how few rows are actually rendered.
func (tr *tableRenderer) visibleRowWindow(offsetRow, totalDataRows int) (start, end int) {
	start = max(offsetRow-rowRenderMargin, 0)
	end = min(offsetRow+tr.visibleDataRows()+1+rowRenderMargin, totalDataRows)
	return start, end
}

// ensureRenderedForSize re-renders the table when the Ping Monitor pane's
// inner dimensions have changed since the last update() call (e.g. initial
// layout, terminal resize, pane fold/maximize).
func (tr *tableRenderer) ensureRenderedForSize() {
	_, _, w, h := tr.tablePane.GetInnerRect()
	if w != tr.lastRenderWidth || h != tr.lastRenderHeight {
		tr.update()
	}
}

// fetchViews takes one GetView() snapshot per target, in order. Called once
// per tick (or once at construction) so every consumer within that tick —
// width calculation, layout planning, row rendering — shares the same
// snapshot instead of each re-fetching it independently.
func fetchViews(targets []*stats.TargetStats) []stats.TargetView {
	views := make([]stats.TargetView, len(targets))
	for i, t := range targets {
		views[i] = t.GetView()
	}
	return views
}

// update re-renders the Ping Monitor table (flat or grouped) and every side pane, for one refresh tick.
func (tr *tableRenderer) update() {
	if tr.beforeUpdate != nil {
		tr.beforeUpdate()
	}
	_, _, tr.lastRenderWidth, tr.lastRenderHeight = tr.tablePane.GetInnerRect()
	tr.table.Clear()

	// One GetView() snapshot per target for this whole tick — width calc,
	// layout planning, and row rendering all read from this same slice
	// instead of each calling GetView() independently: GetView() copies the
	// full RTT history ring, so one call per target per tick is the budget.
	views := fetchViews(tr.targets)
	rowCtx, texts := tr.renderTexts(views)
	tr.chooseLayout(texts)
	if tr.selectionEnabled {
		tr.reconcileSelection()
	}
	// Only the visible (plus margin) row window is rendered — see
	// visibleRowWindow. rowCount stays the full logical count regardless,
	// so reconcileSelection's offset clamping is unaffected.
	offsetRow, _ := tr.table.GetOffset()

	setHeaderRow(tr.table, 0, tr.activeHeaders, tr.widths, tr.activeAligns, tr.headerColor)
	// The title sits on the top border between its corners and padding.
	tr.tablePane.SetTitle(tr.paneTitle(texts, max(tr.lastRenderWidth-2, 0)))
	tr.scanTargets(views, rowCtx)
	tr.renderRows(rowCtx, texts, offsetRow)

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

// chooseLayout fits the columns to the pane (see planColumns) and sets the
// active columns, headers, widths and row count. When not even the core
// columns fit, the previous tick's layout stays.
func (tr *tableRenderer) chooseLayout(texts [][]string) {
	_, _, availableTableWidth, _ := tr.tablePane.GetInnerRect()
	if plan := tr.planColumns(texts, availableTableWidth); plan.ok {
		tr.active = plan.active
		tr.hidden = plan.hidden
		tr.widths = plan.widths
		tr.activeCols = make([]column, len(plan.active))
		tr.activeHeaders = make([]string, len(plan.active))
		tr.activeAligns = make([]int, len(plan.active))
		for j, i := range plan.active {
			tr.activeCols[j] = tr.cols[i]
			tr.activeHeaders[j] = tr.fullHeaders[i]
			tr.activeAligns[j] = tr.fullAligns[i]
		}
	}
	tr.rowCount = len(tr.targets) + 1
	if len(tr.groups) > 0 {
		tr.groupRowMap = buildGroupRows(tr.targets, tr.groups)
		tr.rowCount = len(tr.groupRowMap) + 1
	}
}

// renderTexts renders every target's cell texts for all columns once per
// tick, so layout planning and row rendering share them.
func (tr *tableRenderer) renderTexts(views []stats.TargetView) ([]columnRowContext, [][]string) {
	rowCtx := make([]columnRowContext, len(views))
	texts := make([][]string, len(views))
	for i, view := range views {
		rowCtx[i] = columnRowContext{
			view: view, sourceIPv4: tr.sourceIPv4, sourceIPv6: tr.sourceIPv6,
			packetSize: tr.packetSize, lossRate: calcLossRate(view),
		}
		texts[i] = renderRowTexts(tr.cols, rowCtx[i])
	}
	return rowCtx, texts
}

// scanTargets logs new losses and alert transitions for every target.
func (tr *tableRenderer) scanTargets(views []stats.TargetView, rowCtx []columnRowContext) {
	now := time.Now()
	for i := range tr.targets {
		view := views[i]
		rowSourceIP := displaySourceIPForDst(view.IP, tr.sourceIPv4, tr.sourceIPv6)
		if !view.Health.Configured && !view.LastLossTime.IsZero() {
			lastTime, exists := tr.vs.lastLossTimes[targetViewKey(view)]
			if !exists || view.LastLossTime.After(lastTime) {
				tr.vs.lastLossTimes[targetViewKey(view)] = view.LastLossTime
				tr.vs.appendLog(buildErrorLogMessage(view, rowSourceIP, view.LastError, view.LastLossTime))
			}
		}
		state, msgs := updateAlertState(view, rowSourceIP, rowCtx[i].lossRate, now, tr.vs.alertState[targetViewKey(view)])
		for _, msg := range msgs {
			tr.vs.appendLog(msg)
		}
		tr.vs.alertState[targetViewKey(view)] = state
	}
}

// activeTexts picks the shown columns' texts out of one row's full texts.
func (tr *tableRenderer) activeTexts(row []string) []string {
	out := make([]string, len(tr.active))
	for j, i := range tr.active {
		out[j] = row[i]
	}
	return out
}

// renderRows renders the table rows, flat or grouped. Grouped rows are
// windowed by table-row index (tr.groupRowMap), not target index, since
// group header rows don't correspond to a target — a header inside the
// window must render even if its members don't.
func (tr *tableRenderer) renderRows(rowCtx []columnRowContext, texts [][]string, offsetRow int) {
	if len(tr.groups) == 0 {
		start, end := tr.visibleRowWindow(offsetRow, len(tr.targets))
		for i := start; i < end; i++ {
			tr.setRow(i+1, renderRowCells(tr.activeCols, tr.activeTexts(texts[i]), tr.widths, tr.activeAligns, rowCtx[i], tr.rowColor))
		}
		return
	}
	start, end := tr.visibleRowWindow(offsetRow, len(tr.groupRowMap))
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
			tr.setRow(tableRow, renderRowCells(tr.activeCols, tr.activeTexts(texts[row.targetIdx]), tr.widths, tr.activeAligns, rowCtx[row.targetIdx], tr.rowColor))
		}
	}
}

func (tr *tableRenderer) setRow(row int, cells []*tview.TableCell) {
	for c, cell := range cells {
		tr.table.SetCell(row, c, cell)
	}
}
