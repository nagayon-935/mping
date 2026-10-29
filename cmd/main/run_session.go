package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
)

// session is the state of one run() invocation that outlives its reload
// iterations: what was started once at startup, and the configuration,
// hosts and network settings the next iteration is built from.
type session struct {
	errOut    io.Writer
	startup   config // as started; startup-only settings are read from here
	logWriter io.Writer
	eventSink func(string, stats.Event)
	webSrc    *web.Source
	webSrv    *web.Server
	runUI     func(ui.RunOptions) error
	rc        *reloadCoordinator
	ids       *pinger.IDAllocator
	startedAt time.Time

	// durationCtx is nil without --duration.
	durationCtx      context.Context
	durationDeadline time.Time

	cfg    config
	hosts  []targetSpec
	groups []ui.TargetGroup
	env    runEnv
	// targets are the last iteration's, for the exit summary.
	targets []*stats.TargetStats
	// activePortSpecsRaw records the running port checks so a rebuild is
	// explained in the next iteration's Log.
	activePortSpecsRaw []string
	pendingWarnings    []string
}

type iterationOutcome int

const (
	iterationReload iterationOutcome = iota
	iterationExit
	iterationFailed
)

// iteration is one pass of the run loop: its supervisor and the values the
// UI is built from.
type iteration struct {
	sup               *supervisor
	interval, timeout time.Duration
	packetSize        int
	preLogs           []string
	// logCh carries route flap and watcher log messages to the TUI Log pane.
	logCh chan string
}

// runIteration measures until the UI returns, then decides whether the loop
// re-enters. Failures are reported on errOut.
func (s *session) runIteration(first bool) iterationOutcome {
	if !first {
		// A full reload rebuilds transport-dependent settings as well as workers.
		if err := s.reresolveNetwork(); err != nil {
			s.webSrc.MarkStopped()
			fmt.Fprintf(s.errOut, "Error applying network settings: %v\n", err)
			return iterationFailed
		}
	}
	it, ok := s.startIteration(first)
	if !ok {
		return iterationFailed
	}
	if !s.runUIUntilDone(it) {
		return iterationFailed
	}
	if !s.prepareReload() {
		s.webSrc.MarkStopped()
		return iterationExit
	}
	return iterationReload
}

// reresolveNetwork re-derives the address family, source addresses and port
// specs from the reloaded configuration.
func (s *session) reresolveNetwork() error {
	s.env.resNetwork = resolveNetwork(s.cfg)
	bindIP, dispV4, dispV6, err := determineSourceIPs(s.cfg, s.hosts)
	if err != nil {
		return err
	}
	s.env.bindIP, s.env.dispV4, s.env.dispV6 = bindIP, dispV4, dispV6
	s.env.portSpecs = nil
	for _, raw := range s.cfg.portSpecs {
		spec, _ := pinger.ParsePortSpec(raw)
		s.env.portSpecs = append(s.env.portSpecs, spec)
	}
	return nil
}

// startIteration builds the iteration's targets, pinger and supervisor,
// starts measuring and publishes the supervisor to the web UI.
func (s *session) startIteration(first bool) (*iteration, bool) {
	cfg, hosts, env := s.cfg, s.hosts, s.env
	it := &iteration{
		interval: time.Duration(cfg.intervalMs) * time.Millisecond,
		timeout:  time.Duration(cfg.timeoutMs) * time.Millisecond,
		// The supervisor's OnFlap callback writes to logCh, so it must exist
		// before the supervisor is constructed.
		logCh: make(chan string, 16),
	}

	s.targets = buildTargetsForIteration(hosts, cfg)
	bind := checkerBindConfig(cfg, env.bindIP)
	customResolver := newCustomResolver(cfg.dnsServer, bind)
	pingerFactory := func(targets []*stats.TargetStats, specs []targetSpec) func(size int) pingerController {
		opts := buildPingerOptions(cfg, env.resNetwork, customResolver, specs)
		opts.IDs = s.ids
		return makePingerFactory(targets, opts, cfg, env.bindIP, s.logWriter)
	}

	ifaceMTU, mtuErr := getInterfaceMTU(cfg.ifaceName, env.bindIP, hosts[0].resolveAddr())
	if mtuErr == nil {
		for _, t := range s.targets {
			t.SetIfaceMTU(ifaceMTU)
		}
	}

	makePinger := pingerFactory(s.targets, hosts)
	it.packetSize, it.preLogs = setupPMTU(makePinger, cfg, ifaceMTU, s.targets, hosts[0].display(), s.errOut)
	it.preLogs = append(it.preLogs, s.pendingWarnings...)
	s.pendingWarnings = nil

	it.sup = newSupervisor(supervisorConfig{
		makePinger: makePinger,
		specs:      hosts, groups: s.groups, config: cfg,
		startedAt: s.startedAt, sourceIPv4: env.dispV4, sourceIPv6: env.dispV6,
		durationLimit: s.startup.duration, durationDeadline: s.durationDeadline,
		network: env.resNetwork, reservedOutputs: []string{s.startup.outputFile, cfg.jsonOutputFile, s.startup.eventOutput},
		eventSink: s.eventSink,
		makeTargetPinger: func(size int, targets []*stats.TargetStats, specs []targetSpec) pingerController {
			return pingerFactory(targets, specs)(size)
		},
		packetSize:   it.packetSize,
		targets:      s.targets,
		interval:     it.interval,
		timeout:      it.timeout,
		portSpecs:    env.portSpecs,
		httpURLs:     cfg.httpURLs,
		bind:         bind,
		countLimited: cfg.count > 0,
		logCh:        it.logCh,
	})
	it.sup.Start()
	if err := it.sup.startPinger(); err != nil {
		fmt.Fprintf(s.errOut, "Error starting pinger: %v\n", err)
		fmt.Fprintln(s.errOut, "This program requires root privileges (sudo) for raw ICMP sockets.")
		it.sup.Shutdown()
		return nil, false
	}

	s.webSrc.Set(newWebProvider(it.sup, cfg, hosts, len(env.portSpecs), it.logCh))
	if s.webSrv != nil {
		it.preLogs = append(it.preLogs, webAnnouncement(s.webSrv))
		if first {
			openBrowser(s.webSrv.ControlURL())
		}
	}
	return it, true
}

// runUIUntilDone runs the UI with the iteration's background watchers, then
// tears the iteration down. It reports false when the UI failed.
func (s *session) runUIUntilDone(it *iteration) bool {
	cfg, sup := s.cfg, it.sup

	// sig is closed to signal UI shutdown, either by the hosts-file watcher
	// or by --duration elapsing.
	sig := newReloadSignal()
	s.rc.tryLive = sup.reloadLive
	onFileChange := func() { s.rc.requestFileReload(sig, cfg.hostsFile, it.logCh) }
	watchCancel, watchDone := startWatcher(cfg.hostsFile, onFileChange, it.logCh)
	jsonCancel, jsonDone := startLiveJSONWriter(cfg.jsonOutputFile, func() []*stats.TargetStats { return sup.liveTargets().Targets }, sup.httpResults, s.errOut)

	// The duration watch converges --duration onto the same sig /
	// ExternalCloseCh path as a reload: it requests no reload, so once the UI
	// returns prepareReload finds none pending and the run exits exactly as
	// it would after a plain 'q' quit.
	stopDurationWatch := watchDurationLimit(s.durationCtx, sig, it.logCh)

	runOpts := buildRunOptions(runOptionsParams{
		targets: s.targets, interval: it.interval, timeout: it.timeout,
		// Each natural count completion sends a notification, including
		// after a restart. Other monitors and duration/reload handling
		// remain active.
		doneCh: sup.finished,
		dispV4: s.env.dispV4, dispV6: s.env.dispV6,
		packetSize: it.packetSize, preLogs: it.preLogs, cfg: cfg,
		sup: sup, sig: sig, logCh: it.logCh,
		currentHosts: s.hosts, currentGroups: s.groups,
	})
	if s.webSrv != nil {
		runOpts.WebControlURL = s.webSrv.ControlURL()
	}
	uiErr := s.runUI(runOpts)
	// Stop accepting browser edits before the host list is captured and the
	// supervisor torn down: an edit accepted from here on would be
	// acknowledged and then lost. The final state (stopped, or running again
	// after a reload) is set later and still reaches open streams.
	s.webSrc.MarkReloading()
	if snap := sup.targetSnap.Load(); snap != nil {
		s.targets, s.hosts, s.groups = snap.targets, snap.specs, snap.groups
	}
	stopDurationWatch()
	if uiErr != nil {
		s.webSrc.MarkStopped()
		fmt.Fprintf(s.errOut, "Error running application: %v\n", uiErr)
	}
	finishIteration(cfg, s.targets, sup, s.errOut, jsonCancel, jsonDone, watchCancel, watchDone)
	return uiErr == nil
}

// prepareReload applies a pending reload request, if any, and queues the Log
// notices the next iteration shows. It reports whether the loop re-enters.
func (s *session) prepareReload() bool {
	hosts, groups, cfg, reload, expandWarning := s.rc.apply(s.cfg, s.hosts, s.groups)
	s.hosts, s.groups, s.cfg = hosts, groups, cfg
	if !reload {
		return false
	}
	s.webSrc.MarkReloading()
	s.pendingWarnings = reloadWarnings(s.startup, s.cfg, s.activePortSpecsRaw, expandWarning)
	s.activePortSpecsRaw = append([]string(nil), s.cfg.portSpecs...)
	if s.cfg.outputFile != s.startup.outputFile {
		s.pendingWarnings = append(s.pendingWarnings, "CSV output changes require restarting mping; continuing the startup writer.")
		s.cfg.outputFile = s.startup.outputFile
	}
	if s.cfg.eventOutput != s.startup.eventOutput {
		s.pendingWarnings = append(s.pendingWarnings, "event-output changes require restarting mping; continuing the startup writer.")
		s.cfg.eventOutput = s.startup.eventOutput
	}
	return true
}
