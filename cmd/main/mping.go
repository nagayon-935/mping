package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
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

func resolveNetwork(cfg config) string {
	if cfg.ipv4Only {
		return "ip4"
	}
	if cfg.ipv6Only {
		return "ip6"
	}
	if !hasIPv6Connectivity() {
		return "ip4"
	}
	return "ip"
}

// writeJSONSnapshot serialises a statistics snapshot to path atomically.
// It writes to a temporary file first, then renames it to path, so readers
// always see a complete file.
func writeJSONSnapshot(path string, targets []*stats.TargetStats, httpResults []*stats.HTTPCheckResult) error {
	snap := stats.BuildSnapshot(targets, httpResults)
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename snapshot: %w", err)
	}
	return nil
}

func determineSourceIPs(cfg config, hosts []targetSpec) (string, string, string, error) {
	bindIP := ""
	displaySourceIPv4 := ""
	displaySourceIPv6 := ""

	if cfg.sourceAddr != "" {
		bindIP = cfg.sourceAddr
		if ip := net.ParseIP(bindIP); ip != nil && ip.To4() == nil {
			displaySourceIPv6 = bindIP
		} else {
			displaySourceIPv4 = bindIP
		}
		return bindIP, displaySourceIPv4, displaySourceIPv6, nil
	}
	if cfg.ifaceName != "" {
		ip, err := getInterfaceIP(cfg.ifaceName, cfg.ipv6Only)
		if err != nil {
			return "", "", "", err
		}
		bindIP = ip
		if parsed := net.ParseIP(bindIP); parsed != nil && parsed.To4() == nil {
			displaySourceIPv6 = bindIP
		} else {
			displaySourceIPv4 = bindIP
		}
		return bindIP, displaySourceIPv4, displaySourceIPv6, nil
	}

	displaySourceIPv4, displaySourceIPv6 = detectAutoSourceIPs(hosts)
	return bindIP, displaySourceIPv4, displaySourceIPv6, nil
}

func initTargets(specs []targetSpec) []*stats.TargetStats {
	targets := make([]*stats.TargetStats, 0, len(specs))
	for _, spec := range specs {
		targets = append(targets, stats.NewTargetStats(spec.display()))
	}
	return targets
}

func setupLogger(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	// Write CSV header only when the file is new (empty).
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat log file %q: %w", path, err)
	}
	if info.Size() == 0 {
		if _, err := f.Write([]byte("Timestamp,Host,IP,Seq,Status,RTT(ms),TTL,Error\n")); err != nil {
			f.Close()
			return nil, fmt.Errorf("write csv header: %w", err)
		}
	}
	return f, nil
}

// newCustomResolver returns a *net.Resolver for --dns-server, or
// net.DefaultResolver when it's unset. bind carries the -S source address and
// -I interface name so name resolution leaves the host by the same path as
// the ICMP probes and the port/HTTP checks; it only takes effect once
// dnsServer picks a specific server to dial — the net.DefaultResolver
// fallback can't be steered this way, so bind is a no-op in that case exactly
// as before this feature.
func newCustomResolver(dnsServer string, bind pinger.BindConfig) *net.Resolver {
	if dnsServer == "" {
		return net.DefaultResolver
	}
	host, port, err := net.SplitHostPort(dnsServer)
	if err != nil {
		host = dnsServer
		port = "53"
	}
	dnsAddress := net.JoinHostPort(host, port)
	dialer := pinger.NewBoundDialer("udp", 2*time.Second, bind)
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "udp", dnsAddress)
		},
	}
}

// makePingerFactory returns a closure that creates a configured pinger instance
// with the given payload size. The returned factory is called each time a new
// pinger is needed (initial start and after restart).
func makePingerFactory(targets []*stats.TargetStats, opts pinger.Options, cfg config, bindIP string, logFile io.Writer) func(size int) pingerController {
	return func(size int) pingerController {
		p := newPinger(targets, opts)
		p.SetSource(bindIP)
		p.SetInterface(cfg.ifaceName)
		p.SetSize(size)
		p.SetCount(cfg.count)
		p.SetResolveInterval(dnsResolveInterval)
		if logFile != nil {
			p.SetLogWriter(logFile)
		}
		return p
	}
}

// setupPMTU runs PMTU discovery using a probe pinger and updates per-target
// sizes. It returns the discovered payload size and any pre-log messages.
func setupPMTU(makePinger func(size int) pingerController, cfg config, ifaceMTU int, targets []*stats.TargetStats, firstHost string, errOut io.Writer) (packetSize int, preLogs []string) {
	packetSize = cfg.packetSize
	if !cfg.mtuEnabled {
		return
	}
	if cfg.ipv6Only {
		fmt.Fprintln(errOut, "Warning: PMTU discovery disabled for IPv6")
		return
	}
	probe := makePinger(cfg.packetSize)
	// probe.Start() is never called — DiscoverMaxPayload opens/closes
	// its own sockets internally. Stop() only closes the done channel
	// (safe to call without Start) and prevents any future goroutine
	// from blocking on it.
	defer probe.Stop()
	startPayload := pmtuMaxPayload
	if ifaceMTU > pmtuHeaderBytes {
		startPayload = ifaceMTU - pmtuHeaderBytes
	}
	maxPayload, bottleneckIP, err := probe.DiscoverMaxPayload(context.Background(), firstHost, startPayload, cfg.packetSize, func(line string) {
		preLogs = append(preLogs, line)
	})
	if err != nil {
		fmt.Fprintf(errOut, "PMTU discovery failed: %v\n", err)
		return
	}
	packetSize = maxPayload
	for _, t := range targets {
		t.SetPMTU(maxPayload)
		if bottleneckIP != "" {
			t.SetPMTUBottleneckIP(bottleneckIP)
		}
	}
	return
}

// setupPortChecker parses port specs and starts a PortChecker if any specs are
// provided. Returns nil if no specs are given. bind is the -S/-I pair the ICMP
// pinger is already bound to (see makePingerFactory), so port checks probe over
// the same egress path.
func setupPortChecker(targets []*stats.TargetStats, portSpecs []pinger.PortSpec, interval, timeout time.Duration, bind pinger.BindConfig) *pinger.PortChecker {
	if len(portSpecs) == 0 {
		return nil
	}
	pc := pinger.NewPortChecker(targets, portSpecs, interval, timeout, bind)
	pc.Start()
	return pc
}

func setupHTTPChecker(urls []string, interval, timeout time.Duration, bind pinger.BindConfig) *pinger.HTTPChecker {
	if len(urls) == 0 {
		return nil
	}
	hc := pinger.NewHTTPChecker(urls, interval, timeout, bind)
	hc.Start()
	return hc
}

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

	rc := newReloadCoordinator(fs, cliCfg, cliHosts)
	currentCfg := cfg
	currentHosts := hosts
	// targets is declared outside the loop so the exit summary can read it.
	var targets []*stats.TargetStats

	// durationCtx, when --duration is set, bounds the whole run() invocation
	// (an overall session alarm, mirroring ping.c's -t) rather than being
	// re-armed per reload iteration — the same one-shot-from-startup
	// treatment bindIP already gets even though ifaceName/sourceAddr are
	// re-synced into currentCfg on every reload above. It is deliberately
	// derived from the pre-loop cfg, not currentCfg, so a YAML reload cannot
	// silently extend or shorten an already-running deadline.
	var durationCtx context.Context
	var durationDeadline time.Time
	if cfg.duration > 0 {
		var cancelDuration context.CancelFunc
		durationCtx, cancelDuration = context.WithTimeout(context.Background(), cfg.duration)
		defer cancelDuration()
		durationDeadline, _ = durationCtx.Deadline()
	}

	// activePortSpecsRaw is the --port / port: value the running port
	// checker was actually built from (env.portSpecs is parsed once and
	// never re-derived on reload; see checkPortReloadDrift, TD-25).
	activePortSpecsRaw := cfg.portSpecs
	var pendingWarnings []string
	sessionIDs := &pinger.IDAllocator{}
	sessionStartedAt := time.Now().UTC()

	// Main run loop (re-entered on YAML reload).
	for {
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
			network: resNetwork, reservedOutputs: []string{cfg.outputFile, currentCfg.jsonOutputFile},
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

		var resetMTR, resetHTTP, resetPort func()
		if currentCfg.mtr {
			resetMTR = sup.resetMTR
		}
		if len(currentCfg.httpURLs) > 0 {
			resetHTTP = sup.resetHTTP
		}
		if len(portSpecs) > 0 {
			resetPort = sup.resetPort
		}

		webSrc.Set(newWebProvider(sup, currentCfg, currentHosts, len(portSpecs)))
		if webSrv != nil {
			preLogs = append(preLogs, "Web UI: "+webSrv.URL())
		}

		// Each natural count completion sends a notification, including after
		// restart. Other monitors and duration/reload handling remain active.
		doneCh := sup.finished

		// sig is closed to signal TUI shutdown, either by the YAML watcher, an
		// in-memory add/delete-host request, or (below) --duration elapsing.
		sig := newReloadSignal()
		onFileChange := func() { rc.requestFileReload(sig, currentCfg.hostsFile, logCh) }
		watchCancel, watchDone := startWatcher(currentCfg.hostsFile, onFileChange, logCh)
		jsonCancel, jsonDone := startLiveJSONWriter(currentCfg.jsonOutputFile, func() []*stats.TargetStats { return sup.liveTargets().Targets }, sup.httpResults, errOut)

		// stopDurationWatch converges --duration onto the same sig/
		// ExternalCloseCh path as a YAML reload: nothing here calls
		// rc.requestFileReload/requestHostsChange, so once uiRun returns,
		// rc.apply() below finds no pending reload and the loop breaks to
		// printExitSummary exactly as it would after a plain 'q' quit.
		stopDurationWatch := watchDurationLimit(durationCtx, sig, logCh)

		runOpts := buildRunOptions(runOptionsParams{
			targets: targets, interval: interval, timeout: timeout, doneCh: doneCh,
			dispV4: displaySourceIPv4, dispV6: displaySourceIPv6,
			packetSize: packetSizeToUse, preLogs: preLogs, cfg: currentCfg,
			portCount: len(portSpecs), sup: sup,
			resetMTR: resetMTR, resetHTTP: resetHTTP, resetPort: resetPort,
			thresholds: currentCfg.thresholds, sig: sig, logCh: logCh, rc: rc,
			currentHosts: currentHosts, currentGroups: currentGroups,
		})
		runOpts.TargetSource = sup.liveTargets
		runOpts.OnAddHost = sup.addHost
		runOpts.OnDeleteHost = sup.deleteHost
		runOpts.OnDeleteTarget = sup.deleteTargetID
		runOpts.OnSaveReport = sup.saveReport
		uiErr := uiRun(runOpts)
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

		var reload bool
		var expandWarning string
		currentHosts, currentGroups, currentCfg, reload, expandWarning = rc.apply(currentCfg, currentHosts, currentGroups)
		if !reload {
			webSrc.MarkStopped()
			break
		}
		webSrc.MarkReloading()
		if webWarning := checkWebReloadDrift(cfg, currentCfg); webWarning != "" {
			pendingWarnings = append(pendingWarnings, webWarning)
		}
		if portWarning := checkPortReloadDrift(activePortSpecsRaw, currentCfg.portSpecs); portWarning != "" {
			pendingWarnings = append(pendingWarnings, portWarning)
		}
		if expandWarning != "" {
			pendingWarnings = append(pendingWarnings, expandWarning)
		}
		// Loop continues: targets are re-initialised with the new currentHosts.
	}

	printExitSummary(out, targets)
	if currentCfg.count > 0 && allTargetsUnresponsive(targets) {
		return exitCodeNoResponse
	}
	return 0
}

// allTargetsUnresponsive reports whether every target finished with zero
// replies. Mirrors the nreceived == 0 check Apple's ping.c/ping6.c use to
// decide their exit code; only consulted when --count bounds the run (see
// exitCodeNoResponse) — the always-on TUI mode has no notion of "finished".
func allTargetsUnresponsive(targets []*stats.TargetStats) bool {
	for _, t := range targets {
		if t.GetView().Recv > 0 {
			return false
		}
	}
	return true
}

// printExitSummary writes the per-target ping statistics shown after the TUI exits.
func printExitSummary(out io.Writer, targets []*stats.TargetStats) {
	fmt.Fprintln(out, "\n--- mping statistics ---")
	for _, t := range targets {
		v := t.GetView()
		lossRate := 0.0
		if v.Sent > 0 {
			lossRate = (float64(v.Loss) / float64(v.Sent)) * 100
		}
		fmt.Fprintf(out, "%s (%s): %d packets transmitted, %d received", v.Host, v.IP, v.Sent, v.Recv)
		// Mirrors ping.c's "+N duplicates" / "N packets out of wait time":
		// both are omitted when zero, so a run with neither dups nor late
		// arrivals prints exactly as it did before this feature existed.
		if v.Duplicates > 0 {
			fmt.Fprintf(out, ", +%d duplicates", v.Duplicates)
		}
		fmt.Fprintf(out, ", %.1f%% packet loss", lossRate)
		if v.LateReplies > 0 {
			fmt.Fprintf(out, ", %d packets out of wait time", v.LateReplies)
		}
		fmt.Fprintln(out)
		if v.Recv > 0 {
			fmt.Fprintf(out, "rtt min/avg/max/stddev = %.3f/%.3f/%.3f/%.3f ms\n",
				float64(v.MinRTT.Microseconds())/1000.0,
				float64(v.AvgRTT.Microseconds())/1000.0,
				float64(v.MaxRTT.Microseconds())/1000.0,
				float64(v.StdDev.Microseconds())/1000.0)
		}
		fmt.Fprintln(out)
	}
}

type tracer interface {
	TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error)
}

func runTraceroutes(ctx context.Context, p tracer, targets []*stats.TargetStats) {
	ticker := time.NewTicker(tracerouteInterval)
	defer ticker.Stop()

	runOnce := func() {
		if ctx.Err() != nil {
			return
		}
		for _, t := range targets {
			if len(t.GetView().TraceHops) == 0 {
				t.SetTraceHops([]string{"Tracing..."})
			}
		}

		var wg sync.WaitGroup
		for _, t := range targets {
			wg.Add(1)
			go func(t *stats.TargetStats) {
				defer wg.Done()
				var hops []string
				var err error
				hops, err = p.TraceRoute(ctx, t.Host, tracerouteMaxHops, tracerouteHopTimeout)
				if ctx.Err() != nil {
					return // a cancelled run must not replace the displayed route
				}
				if err != nil {
					t.SetTraceHops([]string{"error: " + err.Error()})
					return
				}
				if len(hops) == 0 {
					t.SetTraceHops([]string{"no route found"})
					return
				}
				t.SetTraceHops(hops)
			}(t)
		}
		wg.Wait()
	}

	runOnce() // Initial run

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func expandTargets(specs []targetSpec, groups []ui.TargetGroup, cfg config) ([]targetSpec, []ui.TargetGroup, error) {
	if !cfg.resolveAll {
		return specs, groups, nil
	}

	resolver := newCustomResolver(cfg.dnsServer, resolverBindConfig(cfg, specs))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resolvedIPs := make(map[string][]string)
	for _, spec := range specs {
		if spec.PinnedIP != "" {
			continue
		}
		rawHost := spec.Host
		if _, err := netip.ParseAddr(rawHost); err == nil {
			resolvedIPs[spec.Host] = []string{rawHost}
			continue
		}

		network := resolveNetwork(cfg)
		ips, err := resolver.LookupIP(ctx, network, rawHost)
		if err != nil || len(ips) == 0 {
			resolvedIPs[spec.Host] = []string{rawHost}
			continue
		}

		var ipStrs []string
		for _, ip := range ips {
			ipStrs = append(ipStrs, ip.String())
		}
		resolvedIPs[spec.Host] = ipStrs
	}

	var expandedSpecs []targetSpec
	expansionMap := make(map[int][]int)

	for i, spec := range specs {
		if spec.PinnedIP != "" {
			expandedSpecs = append(expandedSpecs, spec)
			expansionMap[i] = []int{len(expandedSpecs) - 1}
			continue
		}

		ips := resolvedIPs[spec.Host]
		startIdx := len(expandedSpecs)

		for _, ip := range ips {
			if spec.Host != ip {
				expandedSpecs = append(expandedSpecs, targetSpec{Host: spec.Host, PinnedIP: ip, DSCP: spec.DSCP})
			} else {
				expandedSpecs = append(expandedSpecs, targetSpec{Host: ip, DSCP: spec.DSCP})
			}
		}

		endIdx := len(expandedSpecs)
		var indices []int
		for j := startIdx; j < endIdx; j++ {
			indices = append(indices, j)
		}
		expansionMap[i] = indices
	}

	var expandedGroups []ui.TargetGroup
	for _, g := range groups {
		var newIndices []int
		for _, oldIdx := range g.Indices {
			newIndices = append(newIndices, expansionMap[oldIdx]...)
		}
		expandedGroups = append(expandedGroups, ui.TargetGroup{
			Name:    g.Name,
			Indices: newIndices,
		})
	}

	return expandedSpecs, expandedGroups, nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
