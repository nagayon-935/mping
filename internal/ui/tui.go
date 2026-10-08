package ui

import (
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	tableMaxRows  = 10                     // max ping targets shown without scrolling; keeps UI readable at typical terminal heights
	minUIRefresh  = 200 * time.Millisecond // minimum refresh to avoid flicker at very short ping intervals
	fastUIRefresh = 100 * time.Millisecond // UI refresh rate when ping interval < minUIRefresh
)

var newApplication = tview.NewApplication

type TargetSet struct {
	Targets []*stats.TargetStats
	Groups  []TargetGroup
}

// RunOptions contains all parameters for the Run function.
type RunOptions struct {
	Targets      []*stats.TargetStats
	TargetSource func() TargetSet
	Interval     time.Duration
	Timeout      time.Duration
	DoneCh       chan struct{} // receives count-completion notifications; nil means unlimited
	SourceIPv4   string
	SourceIPv6   string
	PacketSize   int
	InitialLogs  []string
	TraceEnabled bool
	MTREnabled   bool
	PortEnabled  bool
	HTTPEnabled  bool
	ASNEnabled   bool
	PTREnabled   bool
	// DSCPEnabled shows the DSCP column (observed TOS/TrafficClass on each
	// target's most recent reply). Same dynamic-column pattern as ASNEnabled/
	// PTREnabled: cmd/main sets it when --dscp or any host's per-target dscp
	// override is configured, so the column only appears when the feature is
	// actually in use.
	DSCPEnabled bool
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
	// OnRestart restarts the pinger and checkers. On failure the UI stays
	// stopped and permits another attempt. All operation callbacks execute
	// FIFO on the session worker; Run joins the worker before returning.
	OnRestart func() error
	// OnReset owns resetting statistics and monitors as one operation. When
	// nil, Run resets statistics itself and uses the individual reset callbacks.
	OnReset      func()
	OnResetTrace func()
	OnResetMTR   func()
	OnResetPort  func()
	OnResetHTTP  func()
	// OnAddHost is called when the user adds a host via the 'a' key dialog.
	// A non-nil error is displayed in the Log pane; nil updates the live target list.
	OnAddHost func(host string) error
	// OnDeleteHost is called when the user deletes a host via the 'd' key dialog.
	// A non-nil error is displayed in the Log pane; nil updates the live target list.
	OnDeleteHost   func(host string) error
	OnDeleteTarget func(id uint64) error
	OnSaveReport   func(path, format string, selectedID uint64) error
	// Groups defines named groups of targets for grouped display.
	// Nil means flat (ungrouped) layout — existing behaviour.
	Groups []TargetGroup
}

// Run starts the TUI application with the given options.
func Run(opts RunOptions) error {
	if opts.Thresholds != nil {
		setActiveThresholds(*opts.Thresholds)
	}
	targets := opts.Targets // replaced by TargetSource before each update
	currentTargets := func() []*stats.TargetStats { return targets }

	app := newApplication()
	table := newPingTable()
	tablePane := newTablePane(table)
	graphView := newGraphPane(targets, opts.Interval)
	header := newHeader(opts.Interval)
	footer := newFooter()
	addHostInput := newHostInput(" Add host: ", tcell.ColorYellow)
	deleteHostInput := newHostInput(" Delete host: ", tcell.ColorRed)
	pages := tview.NewPages().
		AddPage("footer", footer, true, true).
		AddPage("addHost", addHostInput, true, false).
		AddPage("deleteHost", deleteHostInput, true, false)

	// Render state shared between tableRenderer, the key handler, and the
	// monitor pane render closures (TD-51).
	vs := newViewState(newLogView())
	errorView := vs.errorView
	sidePanes := newSidePanes(opts, currentTargets, vs)

	tr := newTableRenderer(targets, opts.SourceIPv4, opts.SourceIPv6, opts.PacketSize, opts.ASNEnabled, opts.PTREnabled, opts.DSCPEnabled, opts.Groups,
		table, tablePane, opts.InitialLogs, vs)
	tr.sidePanes = sidePanes
	tr.selectionEnabled = true
	if opts.TargetSource != nil {
		tr.beforeUpdate = func() {
			set := opts.TargetSource()
			targets = set.Targets
			tr.targets = targets
			tr.groups = set.Groups
			graphView.targets = targets
		}
	}

	updateTickerCh := make(chan time.Duration, 1)

	session := newUISession()
	defer func() { session.Stop(); session.Wait() }()
	wireHostInputs(app, table, pages, addHostInput, deleteHostInput, vs, session, opts.OnAddHost, opts.OnDeleteHost)

	// Keys
	mainLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	mainLayout.SetBackgroundColor(tcell.ColorBlack)
	controls := newPaneControls(app, mainLayout, header, pages, table, tablePane, func() int { return tr.rowCount }, sidePanes, graphView, errorView)
	controls.rebuild()
	root := tview.NewPages().AddPage("main", controls.view(), true, true)
	details := newHostDetails(opts)
	root.AddPage("details", details.pane, true, false)
	var reportDialog *saveDialog
	var deleteConfirmation *deleteDialog
	var reportReturnFocus tview.Primitive = table
	closeDetails := func() {
		details.open = false
		if (reportDialog != nil && reportDialog.open) || (deleteConfirmation != nil && deleteConfirmation.open) {
			return
		}
		root.SwitchToPage("main")
		app.SetFocus(table)
	}
	notifyResult := func(message string, failed bool) {
		color := "green"
		if failed {
			color = "red"
		}
		text := "[" + color + "]" + tview.Escape(message) + "[-]"
		vs.appendLog(text)
		footer.SetDynamicColors(true).SetText(text)
		if details.open {
			details.footer.SetDynamicColors(true).SetText(text + " | w: Save | Esc: Back")
		}
	}
	reportDialog = newSaveDialog(app, root, session, opts.OnSaveReport, func() {
		if details.open {
			root.SwitchToPage("details")
			app.SetFocus(details.text)
		} else {
			root.SwitchToPage("main")
			app.SetFocus(reportReturnFocus)
		}
	}, notifyResult)
	tr.afterUpdate = func() {
		if details.open && !details.refresh(targets) {
			closeDetails()
			vs.appendLog("[yellow]Selected target was removed; returned to overview[-]")
		}
	}
	deleteConfirmation = newDeleteDialog(app, root, func(id uint64, host string) {
		if !session.Submit(func() {
			err := opts.OnDeleteTarget(id)
			session.Post(func() {
				if err != nil {
					notifyResult("Delete target: "+err.Error(), true)
				} else {
					notifyResult(host+" を削除しました", false)
				}
				tr.update()
				if err != nil && len(targets) == 1 && targets[0].ID == id {
					deleteConfirmation.show(targets[0], true)
				}
			})
		}) {
			vs.appendLog("[yellow]Operation queue full; please try again[-]")
		}
	}, func(focus tview.Primitive) {
		if !details.open && (focus == details.text || focus == details.graph) {
			root.SwitchToPage("main")
			app.SetFocus(table)
		} else {
			app.SetFocus(focus)
		}
	})
	deleteConfirmation.quit = func() { session.Stop(); app.Stop() }
	deleteSelected := func() {
		if opts.OnDeleteTarget == nil {
			return
		}
		id := tr.selectedID
		if details.open {
			id = details.targetID
		}
		// Refresh membership without substituting a new selection for a
		// target that disappeared before the keypress was handled.
		tr.update()
		if id == 0 {
			id = tr.selectedID
		}
		for _, target := range targets {
			if target.ID == id {
				deleteConfirmation.show(target, len(targets) == 1)
				return
			}
		}
	}
	input := newInputHandler(inputHandlerDeps{
		app:             app,
		table:           table,
		addHostInput:    addHostInput,
		deleteHostInput: deleteHostInput,
		graphView:       graphView,
		footer:          footer,
		sidePanes:       sidePanes,
		pages:           pages,
		targets:         targets,
		rowCount:        &tr.rowCount,
		vs:              vs,
		forceUpdate:     tr.update,
		navigate:        tr.moveSelection,
		openDetails: func() {
			tr.update()
			if target := tr.selectedTarget(); target != nil {
				details.targetID = target.ID
				details.open = true
				details.refresh(targets)
				details.text.ScrollToBeginning()
				details.graph.scrollRow = 0
				root.SwitchToPage("details")
				app.SetFocus(details.text)
			}
		},
		deleteSelected: func() {
			if opts.OnDeleteTarget != nil {
				deleteSelected()
			} else {
				pages.SwitchToPage("deleteHost")
				app.SetFocus(deleteHostInput)
			}
		},
		traceEnabled: opts.TraceEnabled,
		mtrEnabled:   opts.MTREnabled,
		portEnabled:  opts.PortEnabled,
		httpEnabled:  opts.HTTPEnabled,
		onStop:       opts.OnStop,
		onRestart:    opts.OnRestart,
		onReset:      opts.OnReset,
		onResetTrace: opts.OnResetTrace,
		onResetMTR:   opts.OnResetMTR,
		onResetPort:  opts.OnResetPort,
		onResetHTTP:  opts.OnResetHTTP,
		onAddHost:    opts.OnAddHost,
		onDeleteHost: opts.OnDeleteHost,
		session:      session,
	})
	session.bind(app, func(event *tcell.EventKey) *tcell.EventKey {
		if deleteConfirmation.open {
			return deleteConfirmation.handle(event)
		}
		if reportDialog.open {
			return reportDialog.handle(event)
		}
		if event.Rune() == 'w' && app.GetFocus() != addHostInput && app.GetFocus() != deleteHostInput && opts.OnSaveReport != nil {
			id := uint64(0)
			if details.open {
				id = details.targetID
			}
			if !details.open {
				reportReturnFocus = app.GetFocus()
			}
			reportDialog.show(id)
			return nil
		}
		if details.open {
			switch event.Key() {
			case tcell.KeyEscape:
				closeDetails()
				return nil
			case tcell.KeyTab:
				if app.GetFocus() == details.text {
					app.SetFocus(details.graph)
				} else {
					app.SetFocus(details.text)
				}
				return nil
			}
			if event.Rune() == 'd' {
				deleteSelected()
				return nil
			}
			if event.Rune() != 'q' && event.Rune() != 's' && event.Rune() != 'S' && event.Rune() != 'R' {
				return event
			}
		}
		if !details.open && controls.handle(event) {
			return nil
		}
		return input(event)
	})
	startRefreshLoop(app, tr, footer, opts.Interval, updateTickerCh, opts.ExternalLogCh, opts.ExternalCloseCh, opts.DoneCh,
		vs, session)

	err := app.SetRoot(root, true).Run()
	session.Stop() // also covers Ctrl-C, terminal failure, and external app.Stop
	return err
}
