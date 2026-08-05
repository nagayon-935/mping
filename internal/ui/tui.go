package ui

import (
	"fmt"
	"sync"
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
	PTREnabled   bool
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
	// OnRestart restarts the pinger and checkers. It returns an error both
	// for a genuine restart failure and when the supervisor has already been
	// shut down by run(); the key handler distinguishes them by checking
	// appStop rather than inspecting the error, since internal/ui cannot
	// import cmd/main.
	OnRestart    func() error
	OnResetTrace func()
	OnResetMTR   func()
	OnResetPort  func()
	OnResetHTTP  func()
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
	ptrEnabled := opts.PTREnabled
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

	tr := newTableRenderer(targets, sourceIPv4, sourceIPv6, packetSize, asnEnabled, ptrEnabled, groups,
		table, tablePane, initialLogs, vs)
	tr.sidePanes = sidePanes

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

	wireHostInputs(app, table, pages, addHostInput, deleteHostInput, vs, onAddHost, onDeleteHost)

	appStop := make(chan struct{})
	var appStopOnce sync.Once
	closeAppStop := func() { appStopOnce.Do(func() { close(appStop) }) }

	// Keys
	app.SetInputCapture(newInputHandler(inputHandlerDeps{
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
		traceEnabled:    traceEnabled,
		mtrEnabled:      mtrEnabled,
		portEnabled:     portEnabled,
		httpEnabled:     httpEnabled,
		onStop:          onStop,
		onRestart:       onRestart,
		onResetTrace:    onResetTrace,
		onResetMTR:      onResetMTR,
		onResetPort:     onResetPort,
		onResetHTTP:     onResetHTTP,
		onAddHost:       onAddHost,
		onDeleteHost:    onDeleteHost,
		closeAppStop:    closeAppStop,
		appStop:         appStop,
	}))
	startRefreshLoop(app, tr, footer, interval, updateTickerCh, externalLogCh, externalCloseCh, doneCh,
		vs, closeAppStop, appStop)

	flex := buildLayout(header, tablePane, sidePanes, graphView, errorView, pages)

	err := app.SetRoot(flex, true).Run()
	closeAppStop() // fallback: ensure goroutine stops even on non-interactive exit
	return err
}
