package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/hostsfile"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/watcher"
	"github.com/spf13/pflag"
)

// The helpers below hold run()'s parse, setup, per-iteration wiring and
// cleanup steps so run() itself reads as parse → construct → loop control.

// startupParams holds everything parseAndLoadHosts derives from argv and the
// optional YAML hosts file, before any network/logger setup happens.
type startupParams struct {
	cfg      config
	hosts    []targetSpec
	fs       *pflag.FlagSet
	cliCfg   config
	cliHosts []string
	groups   []ui.TargetGroup
}

// parseAndLoadHosts parses CLI args, merges the YAML hosts file (if any),
// expands --resolve-all targets, and validates thresholds. On failure it
// writes the appropriate message to out/errOut itself and returns ok=false
// with the exit code run() should return immediately.
func parseAndLoadHosts(args []string, out, errOut io.Writer) (p startupParams, exitCode int, ok bool) {
	cfg, cliHostsRaw, fs, usage, err := parseArgs(args)
	if err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fmt.Fprint(out, usage)
			return startupParams{}, 0, false
		}
		// The error has to be printed explicitly: usage is only non-empty
		// when fs.Usage() ran (pflag's own errors, missing hosts), so the
		// value-range checks in parseArgs would otherwise exit 1 in silence.
		fmt.Fprintf(errOut, "Error: %v\n", err)
		fmt.Fprint(errOut, usage)
		return startupParams{}, 1, false
	}

	cliCfg := cfg
	cliHosts := append([]string(nil), cliHostsRaw...)

	var groups []ui.TargetGroup
	hosts, groups, cfg, err := mergeHosts(cfg, fs, cliHostsRaw)
	if err != nil {
		fmt.Fprintf(errOut, "Error reading hosts file: %v\n", err)
		return startupParams{}, 1, false
	}
	hosts, groups, err = expandTargets(hosts, groups, cfg)
	if err != nil {
		fmt.Fprintf(errOut, "Error expanding targets: %v\n", err)
		return startupParams{}, 1, false
	}
	if len(hosts) == 0 {
		fmt.Fprint(errOut, usage)
		return startupParams{}, 1, false
	}
	if err := cfg.thresholds.Validate(); err != nil {
		fmt.Fprintf(errOut, "Invalid thresholds: %v\n", err)
		return startupParams{}, 1, false
	}

	return startupParams{cfg: cfg, hosts: hosts, fs: fs, cliCfg: cliCfg, cliHosts: cliHosts, groups: groups}, 0, true
}

// runEnv holds the one-time (non-reloadable) environment run() needs for the
// lifetime of the process: network family, source IP, log file, and parsed
// port specs.
type runEnv struct {
	resNetwork string
	bindIP     string
	dispV4     string
	dispV6     string
	logFile    *os.File
	portSpecs  []pinger.PortSpec
}

// prepareRunEnv resolves the network family and source IP, opens the CSV log
// file, and parses port specs — all one-time setup that reloads reuse as-is.
// On failure it writes to errOut itself and returns ok=false with the exit
// code run() should return immediately. The caller owns closing env.logFile.
func prepareRunEnv(cfg config, hosts []targetSpec, out, errOut io.Writer) (env runEnv, exitCode int, ok bool) {
	env.resNetwork = resolveNetwork(cfg)

	bindIP, dispV4, dispV6, err := determineSourceIPs(cfg, hosts)
	if err != nil {
		fmt.Fprintf(errOut, "Error resolving interface %s: %v\n", cfg.ifaceName, err)
		return runEnv{}, 1, false
	}
	env.bindIP, env.dispV4, env.dispV6 = bindIP, dispV4, dispV6
	if cfg.ifaceName != "" && bindIP != "" {
		fmt.Fprintf(out, "Binding to interface %s (%s)\n", cfg.ifaceName, bindIP)
	}

	logFile, err := setupLogger(cfg.outputFile)
	if err != nil {
		fmt.Fprintf(errOut, "Error opening log file: %v\n", err)
		return runEnv{}, 1, false
	}
	env.logFile = logFile

	for _, raw := range cfg.portSpecs {
		spec, err := pinger.ParsePortSpec(raw)
		if err != nil {
			fmt.Fprintf(errOut, "Invalid port spec %q: %v\n", raw, err)
			return runEnv{}, 1, false
		}
		env.portSpecs = append(env.portSpecs, spec)
	}

	return env, 0, true
}

// checkerBindConfig uses the same source and interface as the ICMP pinger.
// Full reloads re-resolve these values before rebuilding all checkers.
func checkerBindConfig(cfg config, bindIP string) pinger.BindConfig {
	return pinger.BindConfig{Source: bindIP, Interface: cfg.ifaceName}
}

// resolverBindConfig is checkerBindConfig for expandTargets' --resolve-all DNS
// lookups. expandTargets runs at startup (parseAndLoadHosts, before
// prepareRunEnv has resolved bindIP via determineSourceIPs), on every
// hosts-file reload (requestFileReload and apply) and when a host is added
// live, so it resolves bindIP itself instead of receiving it as a parameter.
// An unknown -I interface never gets this far: validateBinding, reached
// through mergeHosts' validateMergedHosts, rejects it before expandTargets
// runs. What is left to fail here is an interface that exists but has no
// usable address, and that error is surfaced authoritatively by
// determineSourceIPs in prepareRunEnv (or at the top of a reload iteration)
// before any pinger starts — duplicating it here would add a second, redundant
// error path, so --resolve-all's DNS lookups simply fall back to unbound.
func resolverBindConfig(cfg config, specs []targetSpec) pinger.BindConfig {
	bindIP, _, _, err := determineSourceIPs(cfg, specs)
	if err != nil {
		return pinger.BindConfig{}
	}
	return checkerBindConfig(cfg, bindIP)
}

// buildTargetsForIteration creates TargetStats for hosts and tags each with
// its display DNS server (used by the UI's DNS column).
func buildTargetsForIteration(specs []targetSpec, cfg config) []*stats.TargetStats {
	targets := initTargets(specs)
	for i, t := range targets {
		t.SetResolveAddress(specs[i].resolveAddr())
		t.ConfigureHealth(healthPolicy(cfg))
		hostName := specs[i].Host
		if net.ParseIP(hostName) == nil {
			if cfg.dnsServer != "" {
				t.SetDNSServer(cfg.dnsServer)
			} else {
				t.SetDNSServer("Default")
			}
		} else {
			t.SetDNSServer("-")
		}
	}
	return targets
}

// buildPingerOptions constructs the pinger.Options for one loop iteration,
// including the address resolver. specs supplies an address lookup (keyed
// by targetSpec.display(), the same string as stats.TargetStats.Host())
// so --resolve-all entries resolve straight to their pinned IP and named
// entries resolve to their configured host rather than their name.
func buildPingerOptions(cfg config, resNetwork string, customResolver *net.Resolver, specs []targetSpec) pinger.Options {
	pinned := make(map[string]string, len(specs))
	for _, s := range specs {
		if label, addr := s.display(), s.resolveAddr(); label != addr {
			pinned[label] = addr
		}
	}

	resolve := func(ctx context.Context, _ string, address string) (*net.IPAddr, error) {
		if ip, ok := pinned[address]; ok {
			address = ip
		}
		resolver := net.DefaultResolver
		if customResolver != nil && cfg.dnsServer != "" {
			resolver = customResolver
		}
		return pinger.ResolveIPAddrContext(ctx, resolver, resNetwork, address)
	}
	return pinger.Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return resolve(context.Background(), network, address)
		},
		ResolveIPAddrContext: resolve,
		Resolver:             customResolver,
		AsnEnabled:           cfg.asnEnabled,
		PtrEnabled:           cfg.ptrEnabled,
	}
}

// checkPortReloadDrift explains that port checks were rebuilt from the new
// configuration. reloadWarnings emits it after a successful reload whose port
// specs differ from the ones the running checkers were built from.
func checkPortReloadDrift(activeRaw, reloadedRaw []string) string {
	if slices.Equal(activeRaw, reloadedRaw) {
		return ""
	}
	return fmt.Sprintf("[yellow][%s] port: change detected in the reloaded config — port checks restarted with updated configuration[-]",
		clock())
}

// startWatcher launches the YAML file watcher goroutine when hostsFile is
// set, watching the hosts file and every include file it names. The include
// set is re-read after each change, so an edit that adds a not-yet-existing
// include (and is rejected) still reloads once that file is created. When
// hostsFile isn't set, it returns a no-op cancel and a pre-closed done
// channel so callers can treat both cases uniformly.
func startWatcher(hostsFile string, onFileChange func(), logCh chan<- string) (cancel func(), done chan struct{}) {
	if hostsFile == "" {
		done = make(chan struct{})
		close(done)
		return func() {}, done
	}
	innerDone := make(chan struct{})
	watchCtx, cancelFn := context.WithCancel(context.Background())
	go func() {
		defer close(innerDone)
		paths := func() []string { return hostsfile.WatchPaths(hostsFile) }
		if err := watcher.WatchPaths(watchCtx, paths, onFileChange); err != nil {
			postLog(logCh, fmt.Sprintf("[red][%s] Watcher error: %v — auto-reload disabled, restart mping to re-enable[-]",
				clock(), err))
		}
	}()
	return cancelFn, innerDone
}

// startLiveJSONWriter launches the periodic JSON snapshot writer goroutine when
// path is set. When it isn't, it still returns a valid cancel func (safe to
// call unconditionally) and a pre-closed done channel.
func startLiveJSONWriter(path string, targets func() []*stats.TargetStats, httpResults func() []*stats.HTTPCheckResult, errOut io.Writer) (cancel func(), done chan struct{}) {
	ctx, cancelFn := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	if path == "" {
		close(doneCh)
		return cancelFn, doneCh
	}
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := writeJSONSnapshot(path, targets(), httpResults()); err != nil {
					fmt.Fprintf(errOut, "Warning: JSON snapshot write failed: %v\n", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return cancelFn, doneCh
}

// watchDurationLimit arms sig to fire once durationCtx's deadline elapses,
// converging --duration onto the same ExternalCloseCh shutdown path a YAML
// reload already uses (see reload_coordinator.go): this function never calls
// rc.requestFileReload, so rc.apply() finds no pending
// reload afterwards and run()'s main loop breaks out to printExitSummary
// instead of re-entering — the same outcome as the user pressing 'q'.
//
// durationCtx is nil when --duration was not set (0, the disabled default),
// in which case this is a no-op returning a no-op stop func. Otherwise it
// starts a goroutine bounded to the current loop iteration: the caller MUST
// invoke the returned stop func once that iteration ends by any other path
// (user quit, reload, error), so the goroutine doesn't outlive the iteration
// waiting on a deadline nothing still cares about.
func watchDurationLimit(durationCtx context.Context, sig *reloadSignal, logCh chan<- string) (stop func()) {
	if durationCtx == nil {
		return func() {}
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(exited)
		select {
		case <-durationCtx.Done():
			postLog(logCh, fmt.Sprintf("[green][%s] Duration limit reached, shutting down...[-]", clock()))
			sig.fire()
		case <-done:
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		<-exited
	}
}

// runOptionsParams bundles the per-iteration values buildRunOptions needs to
// construct ui.RunOptions.
type runOptionsParams struct {
	targets       []*stats.TargetStats
	interval      time.Duration
	timeout       time.Duration
	doneCh        chan struct{}
	dispV4        string
	dispV6        string
	packetSize    int
	preLogs       []string
	cfg           config
	sup           *supervisor
	sig           *reloadSignal
	logCh         chan string
	currentHosts  []targetSpec
	currentGroups []ui.TargetGroup
}

// buildRunOptions assembles the ui.RunOptions for one loop iteration. Every
// operation goes to the iteration's supervisor; host edits apply live,
// without a reload. Reset callbacks exist only for the monitors running.
func buildRunOptions(p runOptionsParams) ui.RunOptions {
	opts := ui.RunOptions{
		Targets:          p.targets,
		TargetSource:     p.sup.liveTargets,
		Interval:         p.interval,
		Timeout:          p.timeout,
		DoneCh:           p.doneCh,
		SourceIPv4:       p.dispV4,
		SourceIPv6:       p.dispV6,
		PacketSize:       p.packetSize,
		InitialLogs:      p.preLogs,
		TraceEnabled:     p.cfg.trace,
		MTUEnabled:       p.cfg.mtuEnabled,
		MTREnabled:       p.cfg.mtr,
		PortEnabled:      len(p.cfg.portSpecs) > 0,
		HTTPEnabled:      len(p.cfg.httpURLs) > 0,
		HTTPResults:      p.sup.httpResults,
		ASNEnabled:       p.cfg.asnEnabled,
		PTREnabled:       p.cfg.ptrEnabled,
		Thresholds:       &p.cfg.thresholds,
		MeasurementState: p.sup.measurementState,
		ExternalCloseCh:  p.sig.ch,
		ExternalLogCh:    p.logCh,
		OnStop:           p.sup.stopAll,
		OnRestart: func() error {
			return p.sup.do(cmdRestart)
		},
		OnReset:        p.sup.resetStats,
		OnResetTrace:   p.sup.resetTrace,
		OnAddHost:      p.sup.addHost,
		OnDeleteHost:   p.sup.deleteHost,
		OnDeleteTarget: p.sup.deleteTargetID,
		OnSaveReport:   p.sup.saveReport,
		Groups:         p.currentGroups,
	}
	if p.cfg.mtr {
		opts.OnResetMTR = p.sup.resetMTR
	}
	if len(p.cfg.httpURLs) > 0 {
		opts.OnResetHTTP = p.sup.resetHTTP
	}
	if len(p.cfg.portSpecs) > 0 {
		opts.OnResetPort = p.sup.resetPort
	}
	return opts
}

// finishIteration performs the standard post-uiRun cleanup: stop the JSON
// writer, join all measurement producers, write their final snapshot, stop
// the command loop, and join the file watcher.
//
// Terminate before exporting so the final snapshot includes every completed
// measurement. Shutdown also terminates defensively and joins the loop.
func finishIteration(cfg config, targets []*stats.TargetStats, sup *supervisor, errOut io.Writer, jsonCancel func(), jsonDone chan struct{}, watchCancel func(), watchDone chan struct{}) {
	jsonCancel()
	<-jsonDone
	_ = sup.do(cmdTerminate)
	if cfg.jsonOutputFile != "" {
		if err := writeJSONSnapshot(cfg.jsonOutputFile, targets, sup.httpResults()); err != nil {
			fmt.Fprintf(errOut, "Warning: Final JSON snapshot write failed: %v\n", err)
		}
	}
	sup.Shutdown()
	watchCancel()
	<-watchDone
}
