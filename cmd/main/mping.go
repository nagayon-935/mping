package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
)

const (
	pmtuMaxPayload       = 9872             // max ICMP payload for 9900-byte jumbo frames (9900 - 20 IP - 8 ICMP)
	pmtuHeaderBytes      = 20 + 8           // IPv4 header (20) + ICMP header (8) subtracted from MTU to get max payload
	dnsResolveInterval   = 60 * time.Second // how often each worker re-resolves the target hostname
	tracerouteInterval   = 10 * time.Minute // how often the background traceroute is re-run
	tracerouteMaxHops    = 30               // RFC 1393 recommended maximum; internet paths rarely exceed 30 hops
	tracerouteHopTimeout = 1 * time.Second  // per-hop timeout for traceroute probes
	probePort            = "80"             // destination port used when detecting the preferred outbound IP

	// exitCodeNoResponse mirrors Apple's ping.c/ping6.c: exit(nreceived == 0
	// ? 2 : 0). Returned only when --count is set (a bounded, script-friendly
	// run) and every target finished with zero replies.
	exitCodeNoResponse = 2
)

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
	cfg, hosts, fs := sp.cfg, sp.hosts, sp.fs
	cliCfg, cliHosts := sp.cliCfg, sp.cliHosts
	currentGroups := sp.groups
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

	env, code, ok := prepareRunEnv(cfg, hosts, out, errOut)
	if !ok {
		return code
	}
	var logWriter io.Writer
	if env.logFile != nil {
		defer env.logFile.Close()
		logWriter = env.logFile
	}
	resNetwork, bindIP := env.resNetwork, env.bindIP
	displaySourceIPv4, displaySourceIPv6 := env.dispV4, env.dispV6
	portSpecs := env.portSpecs

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
	showToken := !cfg.noTUI || isTerminal(out)

	rc := newReloadCoordinator(fs, cliCfg, cliHosts)
	currentCfg := cfg
	currentHosts := hosts
	// targets is declared outside the loop so the exit summary can read it.
	var targets []*stats.TargetStats

	// durationCtx, when --duration is set, bounds the whole run() invocation
	// (an overall session alarm, mirroring ping.c's -t) rather than being
	// re-armed per reload iteration — the same one-shot-from-startup
	// treatment the CSV/event output writers and the web server get (a
	// reload keeps their startup values or warns that a restart is needed),
	// unlike bindIP, which every reload re-resolves from the reloaded
	// ifaceName/sourceAddr. It is deliberately derived from the pre-loop cfg,
	// not currentCfg, so a YAML reload cannot silently extend or shorten an
	// already-running deadline.
	var durationCtx context.Context
	var durationDeadline time.Time
	if cfg.duration > 0 {
		var cancelDuration context.CancelFunc
		durationCtx, cancelDuration = context.WithTimeout(context.Background(), cfg.duration)
		defer cancelDuration()
		durationDeadline, _ = durationCtx.Deadline()
	}

	// Record port changes so a rebuild is explained in the next UI iteration.
	activePortSpecsRaw := cfg.portSpecs
	var pendingWarnings []string
	sessionIDs := &pinger.IDAllocator{}
	sessionStartedAt := time.Now().UTC()

	// Main run loop (re-entered on YAML reload).
	for iteration := 0; ; iteration++ {

		if iteration > 0 {
			// A full reload rebuilds transport-dependent settings as well as workers.
			resNetwork = resolveNetwork(currentCfg)
			var bindErr error
			bindIP, displaySourceIPv4, displaySourceIPv6, bindErr = determineSourceIPs(currentCfg, currentHosts)
			if bindErr != nil {
				webSrc.MarkStopped()
				fmt.Fprintf(errOut, "Error applying network settings: %v\n", bindErr)
				return 1
			}
			portSpecs = nil
			for _, raw := range currentCfg.portSpecs {
				spec, _ := pinger.ParsePortSpec(raw)
				portSpecs = append(portSpecs, spec)
			}
		}

		interval := time.Duration(currentCfg.intervalMs) * time.Millisecond
		timeout := time.Duration(currentCfg.timeoutMs) * time.Millisecond

		targets = buildTargetsForIteration(currentHosts, currentCfg)

		bind := checkerBindConfig(currentCfg, bindIP)
		customResolver := newCustomResolver(currentCfg.dnsServer, bind)
		opts := buildPingerOptions(currentCfg, resNetwork, customResolver, currentHosts)
		opts.IDs = sessionIDs

		ifaceMTU, mtuErr := getInterfaceMTU(currentCfg.ifaceName, bindIP, currentHosts[0].resolveAddr())
		if mtuErr == nil {
			for _, t := range targets {
				t.SetIfaceMTU(ifaceMTU)
			}
		}

		makePinger := makePingerFactory(targets, opts, currentCfg, bindIP, logWriter)
		packetSizeToUse, preLogs := setupPMTU(makePinger, currentCfg, ifaceMTU, targets, currentHosts[0].display(), errOut)
		if len(pendingWarnings) > 0 {
			preLogs = append(preLogs, pendingWarnings...)
			pendingWarnings = nil
		}

		// logCh carries route flap and watcher log messages to the TUI Log
		// pane; it must exist before the supervisor (whose OnFlap callback
		// writes to it) is constructed.
		logCh := make(chan string, 16)

		sup := newSupervisor(supervisorConfig{
			makePinger: makePinger,
			specs:      currentHosts, groups: currentGroups, config: currentCfg,
			startedAt: sessionStartedAt, sourceIPv4: displaySourceIPv4, sourceIPv6: displaySourceIPv6,
			durationLimit: cfg.duration, durationDeadline: durationDeadline,
			network: resNetwork, reservedOutputs: []string{cfg.outputFile, currentCfg.jsonOutputFile, cfg.eventOutput},
			eventSink: eventSink,
			makeTargetPinger: func(size int, targets []*stats.TargetStats, specs []targetSpec) pingerController {
				options := buildPingerOptions(currentCfg, resNetwork, customResolver, specs)
				options.IDs = sessionIDs
				return makePingerFactory(targets, options, currentCfg, bindIP, logWriter)(size)
			},
			packetSize:   packetSizeToUse,
			targets:      targets,
			interval:     interval,
			timeout:      timeout,
			portSpecs:    portSpecs,
			httpURLs:     currentCfg.httpURLs,
			bind:         bind,
			traceEnabled: currentCfg.trace,
			mtrEnabled:   currentCfg.mtr,
			countLimited: currentCfg.count > 0,
			logCh:        logCh,
		})

		sup.Start()

		if err := sup.startPinger(); err != nil {
			fmt.Fprintf(errOut, "Error starting pinger: %v\n", err)
			fmt.Fprintln(errOut, "This program requires root privileges (sudo) for raw ICMP sockets.")
			sup.Shutdown()
			return 1
		}

		webSrc.Set(newWebProvider(sup, currentCfg, currentHosts, len(portSpecs), logCh))
		if webSrv != nil {
			preLogs = append(preLogs, webAnnouncement(webSrv, showToken))
			if iteration == 0 && showToken {
				openBrowser(webSrv.ControlURL())
			}
		}

		// Each natural count completion sends a notification, including after
		// restart. Other monitors and duration/reload handling remain active.
		doneCh := sup.finished

		// sig is closed to signal TUI shutdown, either by the YAML watcher or
		// (below) --duration elapsing.
		sig := newReloadSignal()
		rc.tryLive = sup.reloadLive
		onFileChange := func() { rc.requestFileReload(sig, currentCfg.hostsFile, logCh) }
		watchCancel, watchDone := startWatcher(currentCfg.hostsFile, onFileChange, logCh)
		jsonCancel, jsonDone := startLiveJSONWriter(currentCfg.jsonOutputFile, func() []*stats.TargetStats { return sup.liveTargets().Targets }, sup.httpResults, errOut)

		// stopDurationWatch converges --duration onto the same sig/
		// ExternalCloseCh path as a YAML reload: nothing here calls
		// rc.requestFileReload, so once uiRun returns,
		// rc.apply() below finds no pending reload and the loop breaks to
		// printExitSummary exactly as it would after a plain 'q' quit.
		stopDurationWatch := watchDurationLimit(durationCtx, sig, logCh)

		runOpts := buildRunOptions(runOptionsParams{
			targets: targets, interval: interval, timeout: timeout, doneCh: doneCh,
			dispV4: displaySourceIPv4, dispV6: displaySourceIPv6,
			packetSize: packetSizeToUse, preLogs: preLogs, cfg: currentCfg,
			sup: sup, sig: sig, logCh: logCh,
			currentHosts: currentHosts, currentGroups: currentGroups,
		})
		if webSrv != nil && !cfg.noTUI {
			runOpts.WebControlURL = webSrv.ControlURL()
		}
		uiErr := runUI(runOpts)
		// Stop accepting browser edits before the host list is captured and
		// the supervisor torn down: an edit accepted from here on would be
		// acknowledged and then lost. The final state (stopped, or running
		// again after a reload) is set below and still reaches open streams.
		webSrc.MarkReloading()
		if snap := sup.targetSnap.Load(); snap != nil {
			targets = snap.targets
			currentHosts = snap.specs
			currentGroups = snap.groups
		}
		stopDurationWatch()
		if uiErr != nil {
			webSrc.MarkStopped()
			fmt.Fprintf(errOut, "Error running application: %v\n", uiErr)
			finishIteration(currentCfg, targets, sup, errOut, jsonCancel, jsonDone, watchCancel, watchDone)
			return 1
		}

		finishIteration(currentCfg, targets, sup, errOut, jsonCancel, jsonDone, watchCancel, watchDone)

		// A signal means exit, even if a hosts-file change armed a reload
		// while the iteration was ending.
		if headless != nil && headless.quitRequested() {
			webSrc.MarkStopped()
			break
		}

		var reload bool
		var expandWarning string
		currentHosts, currentGroups, currentCfg, reload, expandWarning = rc.apply(currentCfg, currentHosts, currentGroups)
		if !reload {
			webSrc.MarkStopped()
			break
		}
		webSrc.MarkReloading()
		pendingWarnings = reloadWarnings(cfg, currentCfg, activePortSpecsRaw, expandWarning)
		activePortSpecsRaw = append([]string(nil), currentCfg.portSpecs...)
		if currentCfg.outputFile != cfg.outputFile {
			pendingWarnings = append(pendingWarnings, "CSV output changes require restarting mping; continuing the startup writer.")
			currentCfg.outputFile = cfg.outputFile
		}
		if currentCfg.eventOutput != cfg.eventOutput {
			pendingWarnings = append(pendingWarnings, "event-output changes require restarting mping; continuing the startup writer.")
			currentCfg.eventOutput = cfg.eventOutput
		}
		// Loop continues: targets are re-initialised with the new currentHosts.
	}

	printExitSummary(out, targets)
	if currentCfg.count > 0 && allTargetsUnresponsive(targets) {
		return exitCodeNoResponse
	}
	return 0
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
