package ui

import (
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

func init() {
	// Keep ambiguous-width box-drawing, block and marker glyphs (▁..█, ×, ▲, ·, ▸)
	// at width 1 even under East Asian locales (e.g. ja_JP.UTF-8), matching
	// sparkWidth and strip column calculations.
	runewidth.DefaultCondition.EastAsianWidth = false
}

const (
	tableMinVisibleRows = 10                     // floor for the visible Ping Monitor rows and the PgUp/PgDn page step; taller panes show more
	minUIRefresh        = 200 * time.Millisecond // minimum refresh to avoid flicker at very short ping intervals
	fastUIRefresh       = 100 * time.Millisecond // UI refresh rate when ping interval < minUIRefresh
)

var newApplication = tview.NewApplication

// RouteMode selects what the route pane shows.
type RouteMode int

const (
	// RouteOff disables the route pane at startup; in TargetSet it keeps the current mode.
	RouteOff     RouteMode = iota
	RoutePath              // each target's hops, rediscovered periodically (-T)
	RouteMonitor           // the hops with per-hop loss and latency (-M)
)

// TargetSet is a live snapshot of targets, groups and display configuration
// returned by RunOptions.TargetSource on refresh.
type TargetSet struct {
	Targets    []*stats.TargetStats
	Groups     []TargetGroup
	Thresholds *stats.Thresholds
	// Route switches the route pane between RoutePath and RouteMonitor
	// after a reload. RouteOff keeps the current mode: whether the pane
	// exists is fixed for the run.
	Route RouteMode
}

// RunOptions contains all parameters for the Run function.
type RunOptions struct {
	Targets          []*stats.TargetStats
	TargetSource     func() TargetSet
	MeasurementState func() string
	Interval         time.Duration
	Timeout          time.Duration
	DoneCh           chan struct{} // receives count-completion notifications; nil means unlimited
	SourceIPv4       string
	SourceIPv6       string
	PacketSize       int
	InitialLogs      []string
	// WebControlURL is retained separately from the rotating Log. Empty disables
	// the access dialog and header hint; never include it in exported reports.
	WebControlURL string
	Route         RouteMode
	MTUEnabled    bool
	PortEnabled   bool
	HTTPEnabled   bool
	ASNEnabled    bool
	PTREnabled    bool
	// DNSExplicit, SourceExplicit and SizeExplicit report -d, -S/-I and a
	// non-default -s: the Ping Monitor keeps columns the user asked for
	// longer when the terminal is too narrow for every column.
	DNSExplicit    bool
	SourceExplicit bool
	SizeExplicit   bool
	// HTTPResults returns live HTTP health-check results for the HTTP Monitor
	// pane. Called on every render tick (rather than once at startup) so it
	// reflects a checker swapped in by OnResetHTTP after Run has started.
	// Nil when HTTPEnabled is false.
	HTTPResults func() []*stats.HTTPCheckResult
	// Thresholds colour the HTTP checks, which belong to no target; targets
	// are coloured by the thresholds in their own TargetView. Nil keeps the
	// built-in defaults.
	Thresholds *stats.Thresholds
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
	OnResetRoute func()
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
	// Nil means flat (ungrouped) layout.
	Groups []TargetGroup
}

// Run starts the TUI application with the given options.
func Run(opts RunOptions) error {
	t := newTUI(opts)
	t.session = newUISession()
	defer func() { t.session.Stop(); t.session.Wait() }()
	wireHostInputs(t.app, t.table, t.pages, t.addHostInput, t.deleteHostInput, t.vs, t.session, opts.OnAddHost, opts.OnDeleteHost)
	t.buildLayout()
	t.buildDialogs()
	input := newInputHandler(t.inputDeps())
	t.session.bind(t.app, func(event *tcell.EventKey) *tcell.EventKey { return t.handleKey(event, input) })
	startRefreshLoop(t.app, t.tr, t.footer, opts.Interval, opts.ExternalLogCh, opts.ExternalCloseCh, opts.DoneCh,
		t.vs, t.session)

	err := t.app.SetRoot(t.root, true).Run()
	t.session.Stop() // also covers Ctrl-C, terminal failure, and external app.Stop
	return err
}

// columnOptions derives the Ping Monitor's column options from the run
// options; whether a DNS column exists is decided from the targets.
func (o RunOptions) columnOptions() columnOptions {
	return columnOptions{
		DNSExplicit:    o.DNSExplicit,
		ASN:            o.ASNEnabled,
		PTR:            o.PTREnabled,
		SourceExplicit: o.SourceExplicit,
		SizeExplicit:   o.SizeExplicit,
		MTU:            o.MTUEnabled,
		SparkPoints:    sparkPoints(o.Interval),
	}
}

// sparkSeconds is the window of the Ping Monitor's RTT sparkline.
const sparkSeconds = 60

// sparkPoints is how many samples taken every interval span sparkSeconds.
func sparkPoints(interval time.Duration) int {
	if interval <= 0 {
		return 0
	}
	return max(int(sparkSeconds*time.Second/interval), 1)
}
