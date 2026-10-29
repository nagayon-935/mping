package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/nagayon-935/mping/internal/report"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
	"github.com/rivo/tview"
)

// webStart and openBrowser are seams for tests, mirroring uiRun.
var (
	webStart    = web.Start
	openBrowser = defaultOpenBrowser
)

// defaultOpenBrowser launches the user's default browser on targetURL in the
// background (best-effort). When mping runs under sudo or setuid-root,
// privileges are dropped so the browser is never started as root.
func defaultOpenBrowser(targetURL string) {
	_ = asRealUser(func() error {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", targetURL)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
		default:
			if os.Geteuid() == 0 {
				if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
					cmd = exec.Command("sudo", "-u", sudoUser, "xdg-open", targetURL)
					break
				}
			}
			cmd = exec.Command("xdg-open", targetURL)
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
		return nil
	})
}

// startWebUI starts the dashboard when --web is set, returning a nil server
// when it is off. ok=false means it was requested but could not start; the
// error has already been written to errOut.
func startWebUI(cfg config, src *web.Source, errOut io.Writer) (srv *web.Server, ok bool) {
	if !cfg.webEnabled {
		return nil, true
	}
	token := os.Getenv("MPING_WEB_TOKEN")
	if token != "" {
		if err := web.CheckToken(token); err != nil {
			fmt.Fprintf(errOut, "Error starting web UI: MPING_WEB_TOKEN: %v\n", err)
			return nil, false
		}
	}
	srv, err := webStart(web.Options{Port: cfg.webPort, Source: src, Token: token})
	if err != nil {
		fmt.Fprintf(errOut, "Error starting web UI: %v\n", err)
		return nil, false
	}
	return srv, true
}

// webAnnouncement is the Log line offering the web UI. The control link
// carries the token, so it is printed only where it stays private: the TUI,
// or a headless run writing to an interactive terminal (not a pipe, file or
// service journal).
func webAnnouncement(srv *web.Server, showToken bool) string {
	text := "Web UI: " + srv.URL() + " (read-only: the control link is printed only to a terminal; to make changes, set MPING_WEB_TOKEN as described in the README)"
	if showToken {
		text = "Web UI: " + srv.ControlURL()
	}
	return fmt.Sprintf("[blue][%s] %s[-]", clock(), tview.Escape(text))
}

// closeWebUI stops srv (nil-safe). A shutdown error only means a stream
// had to be cut off; it never changes mping's exit status.
func closeWebUI(srv *web.Server, errOut io.Writer) {
	if srv == nil {
		return
	}
	if err := srv.Close(); err != nil {
		fmt.Fprintf(errOut, "Warning: %v\n", err)
	}
}

// checkWebReloadDrift warns when a reload changes web:/web-port:. The web
// server is started once from the startup config (active) and never moved,
// so like checkPortReloadDrift this is a "restart required" nudge.
func checkWebReloadDrift(active, reloaded config) string {
	if active.webEnabled == reloaded.webEnabled &&
		(!active.webEnabled || active.webPort == reloaded.webPort) {
		return ""
	}
	return fmt.Sprintf("[yellow][%s] web: change detected in the reloaded config — the web UI requires a full restart of mping to take effect[-]",
		clock())
}

// webProvider adapts one run-loop iteration's supervisor to web.Provider
// and, through webController, to web.Controller.
type webProvider struct {
	webController
	targets     func() ui.TargetSet
	httpResults func() []*stats.HTTPCheckResult
	base        web.Meta
}

func newWebProvider(sup *supervisor, cfg config, hosts []targetSpec, portCount int, logCh chan<- string) webProvider {
	return webProvider{
		webController: webController{sup: sup, logCh: logCh},
		targets:       sup.liveTargets,
		httpResults:   sup.httpResults,
		base:          webMeta(cfg, hosts, portCount),
	}
}

// webController applies browser edits through the same supervisor calls the
// TUI uses (serialised on the supervisor's command loop) and notes each one
// in the TUI Log pane so the terminal user sees changes made elsewhere.
type webController struct {
	sup   *supervisor
	logCh chan<- string
}

func (c webController) AddHost(host string) error {
	if err := c.sup.addHost(host); err != nil {
		return err
	}
	c.logf("added host %s", host)
	return nil
}

func (c webController) DeleteTarget(id uint64) error {
	host := fmt.Sprintf("#%d", id)
	for _, t := range c.sup.liveTargets().Targets {
		if t.ID() == id {
			host = t.DisplayName()
		}
	}
	if err := c.sup.deleteTargetID(id); err != nil {
		return err
	}
	c.logf("deleted %s", host)
	return nil
}

func (c webController) ResetStats() {
	c.sup.resetStats()
	c.logf("reset statistics")
}

func (c webController) StopMeasurements() error {
	if err := c.sup.do(cmdStop); err != nil {
		return err
	}
	c.logf("stopped measurements")
	return nil
}

func (c webController) RestartMeasurements() error {
	if err := c.sup.do(cmdRestart); err != nil {
		return err
	}
	c.logf("restarted measurements")
	return nil
}

func (c webController) CaptureReport() (snapshot report.Report, err error) {
	err = c.sup.editTargets(func(s *supervisor) error { var e error; snapshot, e = s.captureReport(0); return e })
	return
}

// logf posts to the TUI Log pane without ever blocking a web request on a
// busy terminal; a dropped line only loses the notice, not the edit. Values
// are escaped so a host like "[red]x" cannot inject tview colour tags.
func (c webController) logf(format string, args ...any) {
	postLog(c.logCh, fmt.Sprintf("[blue][%s] web: %s[-]", clock(), tview.Escape(fmt.Sprintf(format, args...))))
}

func (p webProvider) Targets() []*stats.TargetStats         { return p.targets().Targets }
func (p webProvider) HTTPResults() []*stats.HTTPCheckResult { return p.httpResults() }

// Meta re-derives groups on every call: hosts added or deleted at runtime
// change which target IDs each group holds.
func (p webProvider) Meta() web.Meta {
	m := p.base
	if snap := p.sup.targetSnap.Load(); snap != nil {
		m = webMeta(snap.config, snap.specs, len(snap.config.portSpecs))
		m.MeasurementState = snap.measurementState
	}
	m.Groups = webGroups(p.targets())
	return m
}

// webMeta captures the per-iteration settings the browser renders with;
// portCount is the number of parsed --port specs (env.portSpecs).
func webMeta(cfg config, hosts []targetSpec, portCount int) web.Meta {
	th := cfg.thresholds
	return web.Meta{
		IntervalMs: float64(cfg.intervalMs),
		Features: web.Features{
			Traceroute: cfg.trace,
			MTR:        cfg.mtr,
			Port:       portCount > 0,
			HTTP:       len(cfg.httpURLs) > 0,
			ASN:        cfg.asnEnabled,
			PTR:        cfg.ptrEnabled,
			MTU:        cfg.mtuEnabled,
			DSCP:       dscpColumnEnabled(cfg, hosts),
		},
		Thresholds: web.Thresholds{
			RTTWarnMs:    durationMs(th.RTTWarn),
			RTTCritMs:    durationMs(th.RTTCrit),
			JitterWarnMs: durationMs(th.JitterWarn),
			JitterCritMs: durationMs(th.JitterCrit),
			LossWarnPct:  th.LossWarn,
			LossCritPct:  th.LossCrit,
		},
	}
}

// webGroups converts the UI's index-based groups to ID-based ones, dropping
// indices that no longer point at a live target.
func webGroups(set ui.TargetSet) []web.Group {
	groups := make([]web.Group, 0, len(set.Groups))
	for _, g := range set.Groups {
		ids := make([]uint64, 0, len(g.Indices))
		for _, i := range g.Indices {
			if i >= 0 && i < len(set.Targets) {
				ids = append(ids, set.Targets[i].ID())
			}
		}
		groups = append(groups, web.Group{Name: g.Name, TargetIDs: ids})
	}
	return groups
}

func durationMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
