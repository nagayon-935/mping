package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
)

const (
	pmtuMaxPayload       = 9872             // max ICMP payload for 9900-byte jumbo frames (9900 - 20 IP - 8 ICMP)
	pmtuHeaderBytes      = 20 + 8           // IPv4 header (20) + ICMP header (8) subtracted from MTU to get max payload
	dnsResolveInterval   = 60 * time.Second // how often each worker re-resolves the target hostname
	tracerouteMaxHops    = 30               // RFC 1393 recommended maximum; internet paths rarely exceed 30 hops
	tracerouteHopTimeout = 1 * time.Second  // per-hop timeout for traceroute probes
	probePort            = "80"             // destination port used when detecting the preferred outbound IP

	// exitCodeNoResponse mirrors Apple's ping.c/ping6.c: exit(nreceived == 0
	// ? 2 : 0). Returned only when --count is set (a bounded, script-friendly
	// run) and every target finished with zero replies.
	exitCodeNoResponse = 2
)

// tracerouteInterval is how often the background traceroute is re-run. A var
// (not const) so tests can shorten it instead of waiting ten minutes.
var tracerouteInterval = 10 * time.Minute

var uiRun = func(opts ui.RunOptions) error { return ui.Run(opts) }

func run(args []string, out io.Writer, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "completion" {
		return runCompletion(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "__complete-interfaces" {
		return runCompleteInterfaces(out)
	}

	sp, code, ok := parseAndLoadHosts(args, out, errOut)
	if !ok {
		return code
	}
	cfg := sp.cfg
	if cfg.checkConfig {
		if err := printCheckedConfig(out, sp); err != nil {
			fmt.Fprintf(errOut, "Error: %v\n", err)
			return 1
		}
		return 0
	}
	eventSink, closeEvents, eventErr := startEventOutput(cfg.eventOutput, errOut)
	if eventErr != nil {
		fmt.Fprintf(errOut, "Error: %v\n", eventErr)
		return 1
	}
	defer closeEvents()

	env, code, ok := prepareRunEnv(cfg, sp.hosts, out, errOut)
	if !ok {
		return code
	}
	var logWriter io.Writer
	if env.logFile != nil {
		defer env.logFile.Close()
		logWriter = env.logFile
	}

	// The web UI outlives reload iterations: it is started once here and
	// each iteration swaps its supervisor into webSrc.
	webSrc := web.NewSource()
	webSrv, ok := startWebUI(cfg, webSrc, errOut)
	if !ok {
		return 1
	}
	defer closeWebUI(webSrv, errOut)

	runUI, headless, stopHeadless := chooseUIRunner(cfg, out, errOut)
	defer stopHeadless()

	s := &session{
		errOut: errOut, startup: cfg, logWriter: logWriter, eventSink: eventSink,
		webSrc: webSrc, webSrv: webSrv, runUI: runUI, headless: headless,
		showToken: !cfg.noTUI || isTerminal(out),
		rc:        newReloadCoordinator(sp.fs, sp.cliCfg, sp.cliHosts),
		ids:       &pinger.IDAllocator{}, startedAt: time.Now().UTC(),
		cfg: cfg, hosts: sp.hosts, groups: sp.groups, env: env,
		activePortSpecsRaw: cfg.portSpecs,
	}

	// The --duration deadline bounds the whole run() invocation (an overall
	// session alarm, mirroring ping.c's -t) rather than being re-armed per
	// reload iteration — the same one-shot-from-startup treatment the
	// CSV/event output writers and the web server get (a reload keeps their
	// startup values or warns that a restart is needed), unlike the source
	// address, which every reload re-resolves from the reloaded
	// ifaceName/sourceAddr. It is derived from the startup cfg so a YAML
	// reload cannot silently extend or shorten an already-running deadline.
	if cfg.duration > 0 {
		var cancelDuration context.CancelFunc
		s.durationCtx, cancelDuration = context.WithTimeout(context.Background(), cfg.duration)
		defer cancelDuration()
		s.durationDeadline, _ = s.durationCtx.Deadline()
	}

	// Main run loop, re-entered on a full reload.
	for first := true; ; first = false {
		switch s.runIteration(first) {
		case iterationFailed:
			return 1
		case iterationExit:
			printExitSummary(out, s.targets)
			if s.cfg.count > 0 && allTargetsUnresponsive(s.targets) {
				return exitCodeNoResponse
			}
			return 0
		}
	}
}

// chooseUIRunner returns the TUI, or with --no-tui a headless runner that
// prints the Log to out and stops on SIGINT/SIGTERM. stop releases the
// signal handlers; headless is nil for the TUI.
func chooseUIRunner(cfg config, out, errOut io.Writer) (runUI func(ui.RunOptions) error, headless *headlessRunner, stop func()) {
	if !cfg.noTUI {
		return uiRun, nil, func() {}
	}
	if !cfg.webEnabled && cfg.jsonOutputFile == "" && cfg.outputFile == "" {
		fmt.Fprintln(errOut, "Note: --no-tui without --web, --json-output or --output prints only log lines and the final summary.")
	}
	sigs, stopSignals := headlessSignals()
	headless = newHeadlessRunner(out, sigs, stopSignals)
	return headless.run, headless, stopSignals
}

// reloadWarnings collects the Log notices for a reload: settings that only
// require a process restart (web), rebuilt port checks and a failed --resolve-all
// re-expansion. startup is the config mping was started with.
func reloadWarnings(startup, reloaded config, activePortSpecs []string, expandWarning string) []string {
	var warnings []string
	for _, w := range []string{
		checkWebReloadDrift(startup, reloaded),
		checkPortReloadDrift(activePortSpecs, reloaded.portSpecs),
		expandWarning,
	} {
		if w != "" {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
