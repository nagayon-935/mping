package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
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
	targets     []*stats.TargetStats
	sources     sourceIPs
	packetSize  int
	sparkPoints int
	groups      []TargetGroup

	cols []column

	headerColor tcell.Color
	rowColor    tcell.Color

	table     *tview.Table
	tablePane *tview.Flex
	sidePanes []*monitorPane

	// Mutable render state, recomputed every tick. active holds the indices
	// into cols shown in the full layout;
	// widths, activeHeaders and activeAligns are parallel to it. hidden names
	// the columns dropped for width, in the order they were dropped.
	active        []int
	hidden        []string
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

// newTableRenderer builds the column schema and primes the initial log lines.
func newTableRenderer(
	targets []*stats.TargetStats, sources sourceIPs, packetSize int, colOpts columnOptions, groups []TargetGroup,
	table *tview.Table, tablePane *tview.Flex, initialLogs []string, vs *viewState,
) *tableRenderer {
	colOpts.DNS = false
	for _, target := range targets {
		if dns := target.GetPingViewWindow(0).DNSServer; dns != "" && dns != "-" {
			colOpts.DNS = true
			break
		}
	}
	tr := &tableRenderer{
		targets: targets, sources: sources, packetSize: packetSize,
		sparkPoints: max(colOpts.SparkPoints, 0),
		groups:      groups,
		cols:        mainTableColumns(colOpts),
		headerColor: tcell.ColorYellow,
		rowColor:    tcell.ColorWhite,
		table:       table, tablePane: tablePane,
		rowCount: len(targets) + 1,
		vs:       vs,
	}

	for _, line := range initialLogs {
		tr.vs.appendLog(line)
	}

	return tr
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
	return max(tableMinVisibleRows, tr.tableInnerHeight()-1) // one line per row below the header
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
	start = min(max(offsetRow-rowRenderMargin, 0), totalDataRows)
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

// fetchRows copies RTT history only for target rows in the render window.
// Group rows can refer to targets out of target order, so mark the members
// needed for this window before collecting the snapshots.
func (tr *tableRenderer) fetchRows(start, end int) []columnRowContext {
	var visibleTargets []bool
	if len(tr.groups) > 0 && tr.sparkPoints > 0 {
		visibleTargets = make([]bool, len(tr.targets))
		for _, row := range tr.groupRowMap[start:end] {
			if row.targetIdx >= 0 {
				visibleTargets[row.targetIdx] = true
			}
		}
	}

	rows := make([]columnRowContext, len(tr.targets))
	for i, target := range tr.targets {
		points := 0
		visible := i >= start && i < end
		if visibleTargets != nil {
			visible = visibleTargets[i]
		}
		if visible {
			points = tr.sparkPoints
		}
		view := target.GetPingViewWindow(points)
		rows[i] = columnRowContext{
			view: view, sourceIP: tr.sources.forDst(view.IP),
			packetSize: tr.packetSize, lossRate: calcLossRate(view),
		}
	}
	return rows
}

// update re-renders the Ping Monitor table (flat or grouped) and every side pane, for one refresh tick.
func (tr *tableRenderer) update() {
	if tr.beforeUpdate != nil {
		tr.beforeUpdate()
	}
	_, _, tr.lastRenderWidth, tr.lastRenderHeight = tr.tablePane.GetInnerRect()
	tr.table.Clear()

	tr.rowCount = len(tr.targets) + 1
	if len(tr.groups) > 0 {
		tr.groupRowMap = buildGroupRows(tr.targets, tr.groups)
		tr.rowCount = len(tr.groupRowMap) + 1
	}
	if tr.selectionEnabled {
		tr.reconcileSelection()
	}
	offset, _ := tr.table.GetOffset()
	start, end := tr.visibleRowWindow(offset, tr.rowCount-1)
	rows := tr.fetchRows(start, end)
	texts := tr.renderLayoutTexts(rows)
	tr.chooseLayout(texts)

	setHeaderRow(tr.table, 0, tr.activeHeaders, tr.widths, tr.activeAligns, tr.headerColor)
	// The title sits on the top border between its corners and padding.
	tr.tablePane.SetTitle(tr.paneTitle(texts, max(tr.lastRenderWidth-2, 0)))
	tr.scanTargets(rows)
	tr.renderRows(rows, texts, start, end)

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

// chooseLayout fits the columns to the pane. When the
// pane is narrower than the core columns' minimum, tview clips the core layout
// to the viewport.
func (tr *tableRenderer) chooseLayout(texts [][]string) {
	plan := tr.planColumns(texts, tr.lastRenderWidth)
	tr.active = plan.active
	tr.hidden = plan.hidden
	tr.widths = plan.widths
	tr.activeHeaders = make([]string, len(plan.active))
	tr.activeAligns = make([]int, len(plan.active))
	for j, i := range plan.active {
		c := tr.cols[i]
		tr.activeHeaders[j] = c.name
		tr.activeAligns[j] = c.align
	}
}

// renderLayoutTexts formats the cells that can influence the column plan or
// pane title. Measurement cells are formatted only for visible rows.
func (tr *tableRenderer) renderLayoutTexts(rows []columnRowContext) [][]string {
	texts := make([][]string, len(rows))
	values := make([]string, len(rows)*len(tr.cols))
	for i := range rows {
		texts[i] = values[i*len(tr.cols) : (i+1)*len(tr.cols)]
	}
	for j, col := range tr.cols {
		if col.affectsLayout() {
			for i := range rows {
				texts[i][j] = col.render(rows[i])
			}
		}
	}
	return texts
}

// scanTargets logs new losses and alert transitions for every target.
func (tr *tableRenderer) scanTargets(rows []columnRowContext) {
	now := time.Now()
	for i := range rows {
		view := rows[i].view
		key := targetViewKey(view)
		rowSourceIP := rows[i].sourceIP
		if !view.Health.Configured && !view.LastLossTime.IsZero() {
			lastTime, exists := tr.vs.lastLossTimes[key]
			if !exists || view.LastLossTime.After(lastTime) {
				tr.vs.lastLossTimes[key] = view.LastLossTime
				tr.vs.appendLog(buildErrorLogMessage(view, rowSourceIP, view.LastFailureReason, view.LastLossTime))
			}
		}
		state, msgs := updateAlertState(view, rowSourceIP, rows[i].lossRate, now, tr.vs.alertState[key])
		for _, msg := range msgs {
			tr.vs.appendLog(msg)
		}
		tr.vs.alertState[key] = state
	}
}

// renderRows renders the table rows, flat or grouped. Grouped rows use
// table-row indices so visible section headers are rendered too.
func (tr *tableRenderer) renderRows(rows []columnRowContext, texts [][]string, start, end int) {
	if len(tr.groups) == 0 {
		for i := start; i < end; i++ {
			tr.renderTargetRow(i+1, rows[i], texts[i])
		}
		return
	}
	for rowIdx := start; rowIdx < end; rowIdx++ {
		row := tr.groupRowMap[rowIdx]
		tableRow := rowIdx + 1
		switch row.kind {
		case groupRowSpacer:
			setGroupSpacerRow(tr.table, tableRow, tr.widths)
		case groupRowHeader:
			setGroupHeaderRow(tr.table, tableRow, tr.widths, tr.groups[row.groupIdx], rows)
		case groupRowSubHeader:
			setHeaderRow(tr.table, tableRow, tr.activeHeaders, tr.widths, tr.activeAligns, tr.headerColor)
		case groupRowUngrouped, groupRowTarget:
			tr.renderTargetRow(tableRow, rows[row.targetIdx], texts[row.targetIdx])
		}
	}
}

func (tr *tableRenderer) renderTargetRow(row int, ctx columnRowContext, texts []string) {
	for j, i := range tr.active {
		col := tr.cols[i]
		text := texts[i]
		width := tr.widths[j]
		if !col.affectsLayout() && (col.fit == nil || width <= boxCellPadding) {
			text = col.render(ctx)
		}
		tr.table.SetCell(row, j, renderCell(col, text, width, tr.activeAligns[j], ctx, tr.rowColor))
	}
}
