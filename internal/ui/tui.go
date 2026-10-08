package ui

import (
	"fmt"
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
	ptrEnabled := opts.PTREnabled
	dscpEnabled := opts.DSCPEnabled
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

	tablePane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true)
	tablePane.SetBorder(true).SetTitle(" Ping Monitor ").SetBorderColor(tcell.ColorWhite)

	// Render state shared between tableRenderer, the key handler, and the
	// monitor pane render closures below (TD-51).
	vs := newViewState(errorView)

	tracePaneObj := newMonitorPane(traceEnabled, " Traceroute Monitor ", func(availW int) string {
		return renderTracerouteTable(targets, availW)
	})
	mtrPaneObj := newMonitorPane(mtrEnabled, " MTR Monitor ", func(availW int) string {
		return renderMTRTable(targets, availW, sourceIPv4, sourceIPv6)
	})
	portPaneObj := newMonitorPane(portEnabled, " Port Monitor ", func(availW int) string {
		return renderPortMonitorTable(targets, availW, vs.lastPortStatuses, &vs.errorLogs, vs.errorView)
	})
	httpPaneObj := newMonitorPane(httpEnabled, " HTTP Monitor ", func(availW int) string {
		var httpResults []*stats.HTTPCheckResult
		if httpResultsFunc != nil {
			httpResults = httpResultsFunc()
		}
		return renderHTTPMonitorTable(httpResults, availW, vs.lastHTTPStatuses, &vs.errorLogs, vs.errorView)
	})
	sidePanes := []*monitorPane{tracePaneObj, mtrPaneObj, portPaneObj, httpPaneObj}

	tr := newTableRenderer(targets, sourceIPv4, sourceIPv6, packetSize, asnEnabled, ptrEnabled, dscpEnabled, groups,
		table, tablePane, initialLogs, vs)
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

	header := tview.NewTextView().
		SetText(fmt.Sprintf("MPING - Multi Ping Tool | Interval: %dms", interval.Milliseconds())).
		SetTextAlign(tview.AlignCenter).
		SetTextColor(tcell.ColorGreen).
		SetWrap(false)
	header.SetBackgroundColor(tcell.ColorBlack)

	footer := tview.NewTextView().
		SetText("Enter Detail | Tab Pane | f Fold | z Max | w Save | a Add | d Del | s Stop | q Quit").
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

	session := newUISession()
	defer func() { session.Stop(); session.Wait() }()
	wireHostInputs(app, table, pages, addHostInput, deleteHostInput, vs, session, onAddHost, onDeleteHost)

	// Keys
	mainLayout := buildLayout(header, tablePane, sidePanes, graphView, errorView, pages)
	controls := newPaneControls(app, mainLayout, header, pages, table, tablePane, sidePanes, graphView, errorView)
	root := tview.NewPages().AddPage("main", mainLayout, true, true)
	details := newHostDetails(opts)
	root.AddPage("details", details.pane, true, false)
	var reportDialog *saveDialog
	var reportReturnFocus tview.Primitive = table
	closeDetails := func() {
		details.open = false
		if reportDialog != nil && reportDialog.open {
			return
		}
		root.SwitchToPage("main")
		app.SetFocus(table)
	}
	reportDialog = newSaveDialog(app, root, session, opts.OnSaveReport, func() {
		if details.open {
			root.SwitchToPage("details")
			app.SetFocus(details.text)
		} else {
			root.SwitchToPage("main")
			app.SetFocus(reportReturnFocus)
		}
	}, func(message string, failed bool) {
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
	})
	tr.afterUpdate = func() {
		if details.open && !details.refresh(targets) {
			closeDetails()
			vs.appendLog("[yellow]Selected target was removed; returned to overview[-]")
		}
	}
	deleteSelected := func() {
		id := tr.selectedID
		if details.open {
			id = details.targetID
		}
		if opts.OnDeleteTarget == nil {
			return
		}
		if !session.Submit(func() {
			err := opts.OnDeleteTarget(id)
			session.Post(func() {
				if err != nil {
					vs.appendLog("[red]Delete target: " + tview.Escape(err.Error()) + "[-]")
				}
				tr.update()
			})
		}) {
			vs.appendLog("[yellow]Operation queue full; please try again[-]")
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
		traceEnabled: traceEnabled,
		mtrEnabled:   mtrEnabled,
		portEnabled:  portEnabled,
		httpEnabled:  httpEnabled,
		onStop:       onStop,
		onRestart:    onRestart,
		onReset:      opts.OnReset,
		onResetTrace: onResetTrace,
		onResetMTR:   onResetMTR,
		onResetPort:  onResetPort,
		onResetHTTP:  onResetHTTP,
		onAddHost:    onAddHost,
		onDeleteHost: onDeleteHost,
		session:      session,
	})
	session.bind(app, func(event *tcell.EventKey) *tcell.EventKey {
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
	startRefreshLoop(app, tr, footer, interval, updateTickerCh, externalLogCh, externalCloseCh, doneCh,
		vs, session)

	err := app.SetRoot(root, true).Run()
	session.Stop() // also covers Ctrl-C, terminal failure, and external app.Stop
	return err
}
