package ui

import (
	"strings"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// tui holds one Run's widgets and the state their callbacks share. Like
// viewState, it is only touched from tview's event/draw goroutine (or
// before app.Run starts).
type tui struct {
	opts RunOptions
	// targets is the live target list, replaced from TargetSource before
	// each table update.
	targets []*stats.TargetStats

	app             *tview.Application
	table           *tview.Table
	tablePane       *tview.Flex
	graphView       *GraphView
	header          *tview.TextView
	footer          *tview.TextView
	addHostInput    *tview.InputField
	deleteHostInput *tview.InputField
	pages           *tview.Pages // footer and host input bar
	root            *tview.Pages // main layout and full-screen pages
	vs              *viewState
	sidePanes       []*monitorPane
	tr              *tableRenderer
	controls        *paneControls
	details         *hostDetails
	session         *uiSession

	reportDialog       *saveDialog
	deleteConfirmation *deleteDialog
	webAccess          *webAccessDialog
	reportReturnFocus  tview.Primitive
}

// newTUI builds the widgets and the table renderer.
func newTUI(opts RunOptions) *tui {
	t := &tui{opts: opts, targets: opts.Targets}
	t.app = newApplication()
	t.table = newPingTable()
	t.tablePane = newTablePane(t.table)
	t.graphView = newGraphPane(t.targets, opts.Interval)
	t.header = newHeader(opts.Interval)
	if opts.WebControlURL != "" {
		base, _, _ := strings.Cut(opts.WebControlURL, "#")
		t.header.SetText(t.header.GetText(false) + "\nWeb: " + base + " | b: URL / Copy")
	}
	t.footer = newFooter()
	t.addHostInput = newHostInput(" Add host: ", tcell.ColorYellow)
	t.deleteHostInput = newHostInput(" Delete host: ", tcell.ColorRed)
	t.pages = tview.NewPages().
		AddPage("footer", t.footer, true, true).
		AddPage("addHost", t.addHostInput, true, false).
		AddPage("deleteHost", t.deleteHostInput, true, false)

	// Render state shared between tableRenderer, the key handler, and the
	// monitor pane render closures.
	t.vs = newViewState(newLogView())
	if opts.Thresholds != nil {
		t.vs.thresholds = *opts.Thresholds
	}
	t.sidePanes = newSidePanes(opts, func() []*stats.TargetStats { return t.targets }, func() RouteMode { return t.opts.Route }, t.vs)

	t.tr = newTableRenderer(t.targets, opts.SourceIPv4, opts.SourceIPv6, opts.PacketSize, opts.ASNEnabled, opts.PTREnabled, opts.Groups,
		t.table, t.tablePane, opts.InitialLogs, t.vs)
	t.tr.sidePanes = t.sidePanes
	t.tr.selectionEnabled = true
	if opts.TargetSource != nil {
		t.tr.beforeUpdate = t.pullTargets
	}
	return t
}

// pullTargets replaces the target list (and the run's thresholds and route
// mode) from TargetSource.
func (t *tui) pullTargets() {
	set := t.opts.TargetSource()
	if set.Thresholds != nil && set.Thresholds.RTTCrit > 0 {
		t.vs.thresholds = *set.Thresholds
	}
	if set.Route != RouteOff && t.opts.Route != RouteOff {
		t.opts.Route = set.Route
		if t.details != nil {
			t.details.opts.Route = set.Route
		}
	}
	t.targets = set.Targets
	t.tr.targets = t.targets
	t.tr.groups = set.Groups
	t.graphView.targets = t.targets
}

// buildLayout assembles the main layout, its pane controls and the host
// details page.
func (t *tui) buildLayout() {
	mainLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	mainLayout.SetBackgroundColor(tcell.ColorBlack)
	t.controls = newPaneControls(t.app, mainLayout, t.header, t.pages, t.table, t.tablePane, func() int { return t.tr.rowCount }, t.sidePanes, t.graphView, t.vs.errorView)
	t.controls.onTableResize = t.tr.ensureRenderedForSize
	t.controls.rebuild()
	t.root = tview.NewPages().AddPage("main", t.controls.view(), true, true)
	t.details = newHostDetails(t.opts)
	t.root.AddPage("details", t.details.pane, true, false)
	t.reportReturnFocus = t.table
	t.tr.afterUpdate = func() {
		if t.details.open && !t.details.refresh(t.targets) {
			t.closeDetails()
			t.vs.appendLog("[yellow]Selected target was removed; returned to overview[-]")
		}
	}
}

// buildDialogs creates the report, delete and web access dialogs.
func (t *tui) buildDialogs() {
	t.reportDialog = newSaveDialog(t.app, t.root, t.session, t.opts.OnSaveReport, func() {
		if t.details.open {
			t.root.SwitchToPage("details")
			t.app.SetFocus(t.details.text)
		} else {
			t.root.SwitchToPage("main")
			t.app.SetFocus(t.reportReturnFocus)
		}
	}, t.notifyResult)
	t.deleteConfirmation = newDeleteDialog(t.app, t.root, t.confirmDelete, t.restoreFocus)
	t.deleteConfirmation.quit = func() { t.session.Stop(); t.app.Stop() }
	t.webAccess = newWebAccessDialog(t.app, t.root, t.opts.WebControlURL, t.session.copyClipboard, t.restoreFocus)
}

// dialogOpen reports whether a dialog is showing.
func (t *tui) dialogOpen() bool {
	return (t.reportDialog != nil && t.reportDialog.open) ||
		(t.deleteConfirmation != nil && t.deleteConfirmation.open) ||
		(t.webAccess != nil && t.webAccess.open)
}

// closeDetails leaves the host details page, unless a dialog is still
// showing over it.
func (t *tui) closeDetails() {
	t.details.open = false
	if t.dialogOpen() {
		return
	}
	t.root.SwitchToPage("main")
	t.app.SetFocus(t.table)
}

// openDetails shows the host details page for the selected target.
func (t *tui) openDetails() {
	t.tr.update()
	if target := t.tr.selectedTarget(); target != nil {
		t.details.targetID = target.ID()
		t.details.open = true
		t.details.refresh(t.targets)
		t.details.text.ScrollToBeginning()
		t.details.graph.scrollRow = 0
		t.root.SwitchToPage("details")
		t.app.SetFocus(t.details.text)
	}
}

// notifyResult reports an operation's outcome in the Log and the footer.
func (t *tui) notifyResult(message string, failed bool) {
	color := "green"
	if failed {
		color = "red"
	}
	text := "[" + color + "]" + tview.Escape(message) + "[-]"
	t.vs.appendLog(text)
	t.footer.SetDynamicColors(true).SetText(text)
	if t.details.open {
		t.details.footer.SetDynamicColors(true).SetText(text + " | w: Save | Esc: Back")
	}
}

// restoreFocus returns focus after a dialog closes, falling back to the
// table when the details page it came from has closed meanwhile.
func (t *tui) restoreFocus(focus tview.Primitive) {
	if !t.details.open && (focus == t.details.text || focus == t.details.graph) {
		t.root.SwitchToPage("main")
		t.app.SetFocus(t.table)
	} else {
		t.app.SetFocus(focus)
	}
}

// confirmDelete runs the confirmed deletion on the session worker and
// reports the result back on the UI goroutine.
func (t *tui) confirmDelete(id uint64, host string) {
	if !t.session.Submit(func() {
		err := t.opts.OnDeleteTarget(id)
		t.session.Post(func() {
			if err != nil {
				t.notifyResult("Delete target: "+err.Error(), true)
			} else {
				t.notifyResult(host+" を削除しました", false)
			}
			t.tr.update()
			if err != nil && len(t.targets) == 1 && t.targets[0].ID() == id {
				t.deleteConfirmation.show(t.targets[0], true)
			}
		})
	}) {
		t.vs.appendLog(queueFullLog)
	}
}

// deleteSelected asks to confirm deleting the selected target (or the one
// whose details are open).
func (t *tui) deleteSelected() {
	if t.opts.OnDeleteTarget == nil {
		return
	}
	id := t.tr.selectedID
	if t.details.open {
		id = t.details.targetID
	}
	// Refresh membership without substituting a new selection for a
	// target that disappeared before the keypress was handled.
	t.tr.update()
	if id == 0 {
		id = t.tr.selectedID
	}
	for _, target := range t.targets {
		if target.ID() == id {
			t.deleteConfirmation.show(target, len(t.targets) == 1)
			return
		}
	}
}

func (t *tui) inputDeps() inputHandlerDeps {
	opts := t.opts
	return inputHandlerDeps{
		app:             t.app,
		table:           t.table,
		addHostInput:    t.addHostInput,
		deleteHostInput: t.deleteHostInput,
		footer:          t.footer,
		pages:           t.pages,
		targets:         t.targets,
		rowCount:        &t.tr.rowCount,
		vs:              t.vs,
		forceUpdate:     t.tr.update,
		navigate:        t.tr.moveSelection,
		openDetails:     t.openDetails,
		deleteSelected: func() {
			if opts.OnDeleteTarget != nil {
				t.deleteSelected()
			} else {
				t.pages.SwitchToPage("deleteHost")
				t.app.SetFocus(t.deleteHostInput)
			}
		},
		routeEnabled:     opts.Route != RouteOff,
		portEnabled:      opts.PortEnabled,
		httpEnabled:      opts.HTTPEnabled,
		onStop:           opts.OnStop,
		measurementState: opts.MeasurementState,
		onRestart:        opts.OnRestart,
		onReset:          opts.OnReset,
		onResetRoute:     opts.OnResetRoute,
		onResetPort:      opts.OnResetPort,
		onResetHTTP:      opts.OnResetHTTP,
		onAddHost:        opts.OnAddHost,
		onDeleteHost:     opts.OnDeleteHost,
		session:          t.session,
	}
}

// handleKey routes a key to the open dialog, the global shortcuts, the
// details page, the pane controls and finally the main input handler.
func (t *tui) handleKey(event *tcell.EventKey, input func(*tcell.EventKey) *tcell.EventKey) *tcell.EventKey {
	switch {
	case t.deleteConfirmation.open:
		return t.deleteConfirmation.handle(event)
	case t.reportDialog.open:
		return t.reportDialog.handle(event)
	case t.webAccess.open:
		return t.webAccess.handle(event)
	}
	typing := t.app.GetFocus() == t.addHostInput || t.app.GetFocus() == t.deleteHostInput
	if event.Rune() == 'b' && !typing && t.opts.WebControlURL != "" {
		t.webAccess.show()
		return nil
	}
	if event.Rune() == 'w' && !typing && t.opts.OnSaveReport != nil {
		id := uint64(0)
		if t.details.open {
			id = t.details.targetID
		} else {
			t.reportReturnFocus = t.app.GetFocus()
		}
		t.reportDialog.show(id)
		return nil
	}
	if t.details.open {
		return t.handleDetailsKey(event, input)
	}
	if t.controls.handle(event) {
		return nil
	}
	return input(event)
}

// handleDetailsKey handles a key on the host details page: navigation and
// delete here, the global q/s/S/R shortcuts via input, everything else
// to the focused widget.
func (t *tui) handleDetailsKey(event *tcell.EventKey, input func(*tcell.EventKey) *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		t.closeDetails()
		return nil
	case tcell.KeyTab:
		if t.app.GetFocus() == t.details.text {
			t.app.SetFocus(t.details.graph)
		} else {
			t.app.SetFocus(t.details.text)
		}
		return nil
	}
	switch event.Rune() {
	case 'd':
		t.deleteSelected()
		return nil
	case 'q', 's', 'S', 'R':
		return input(event)
	}
	return event
}
