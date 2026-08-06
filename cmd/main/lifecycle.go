package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/watcher"
	"github.com/spf13/pflag"
)

// TD-22④: the helpers below carve run()'s former parse/setup/per-iteration
// wiring/cleanup steps out into named functions so run() itself is left as
// parse → construct → loop-control only.

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
	expandedHosts, expandedGroups, err := expandTargets(hosts, groups, cfg)
	if err != nil {
		fmt.Fprintf(errOut, "Error expanding targets: %v\n", err)
		return startupParams{}, 1, false
	}
	hosts = expandedHosts
	groups = expandedGroups
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

// checkerBindConfig builds the source/interface binding for the port and HTTP
// checkers. It deliberately mirrors what makePingerFactory feeds the ICMP
// pinger — SetSource(bindIP) plus SetInterface(cfg.ifaceName) — including the
// asymmetry that bindIP is resolved once at startup while ifaceName is re-read
// from the (possibly reloaded) config, so all three check types always probe
// over the same egress path.
func checkerBindConfig(cfg config, bindIP string) pinger.BindConfig {
	return pinger.BindConfig{Source: bindIP, Interface: cfg.ifaceName}
}

// resolverBindConfig is checkerBindConfig for expandTargets' --resolve-all DNS
// lookups, which run during parseAndLoadHosts — before prepareRunEnv has
// resolved bindIP via determineSourceIPs. It resolves bindIP itself instead of
// receiving it as a parameter. A resolution failure (e.g. an unknown -I
// interface) is swallowed here rather than surfaced: the authoritative error
// for a bad -I is prepareRunEnv's own determineSourceIPs call, which runs
// moments later and stops the process before any pinger starts — duplicating
// that failure here would just add a second, redundant error path for the
// same misconfiguration, so --resolve-all's DNS lookups simply fall back to
// unbound in that case.
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
// including the address resolver. specs supplies a pinned-IP lookup (keyed
// by targetSpec.display(), the same string used for stats.TargetStats.Host)
// so --resolve-all entries resolve straight to their pinned IP.
//
// errors are collected rather than returned as a second value: cfg.dscp and
// every spec's DSCP were already validated (parseArgs / validateHostsDoc), so
// a parse failure here would mean that validation was skipped somewhere —
// buildPingerOptions ignores an unparsable spec (treating it as "no
// override") rather than propagating a signature change through every
// caller for a case that should be unreachable in practice.
func buildPingerOptions(cfg config, resNetwork string, customResolver *net.Resolver, specs []targetSpec) pinger.Options {
	pinned := make(map[string]string, len(specs))
	for _, s := range specs {
		if s.PinnedIP != "" {
			pinned[s.display()] = s.PinnedIP
		}
	}

	var dscp *int
	if cfg.dscp != "" {
		if v, err := pinger.ParseDSCP(cfg.dscp); err == nil {
			dscp = &v
		}
	}
	var targetDSCP map[string]int
	for _, s := range specs {
		if s.DSCP == "" {
			continue
		}
		v, err := pinger.ParseDSCP(s.DSCP)
		if err != nil {
			continue
		}
		if targetDSCP == nil {
			targetDSCP = make(map[string]int, len(specs))
		}
		targetDSCP[s.display()] = v
	}

	return pinger.Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			if ip, ok := pinned[address]; ok {
				return net.ResolveIPAddr(network, ip)
			}
			if customResolver != nil && cfg.dnsServer != "" {
				ips, err := customResolver.LookupIP(context.Background(), network, address)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, &net.DNSError{Err: "no such host", Name: address}
				}
				return &net.IPAddr{IP: ips[0]}, nil
			}
			return net.ResolveIPAddr(resNetwork, address)
		},
		Resolver:   customResolver,
		AsnEnabled: cfg.asnEnabled,
		PtrEnabled: cfg.ptrEnabled,
		DSCP:       dscp,
		TargetDSCP: targetDSCP,
	}
}

// checkPortReloadDrift returns a warning message when a reloaded config
// requests a different --port / port: set than the one the running port
// checker was actually built from. Port specs are parsed once at startup and
// never re-applied mid-run (TD-25), so this is a "restart required" nudge
// rather than a fix: it returns "" when there's no drift.
func checkPortReloadDrift(activeRaw, reloadedRaw []string) string {
	if slices.Equal(activeRaw, reloadedRaw) {
		return ""
	}
	return fmt.Sprintf("[yellow][%s] port: change detected in the reloaded config — port checks require a full restart of mping to take effect[-]",
		time.Now().Format("15:04:05"))
}

// startWatcher launches the YAML file watcher goroutine when hostsFile is
// set. When it isn't, it returns a no-op cancel and a pre-closed done
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
		if err := watcher.Watch(watchCtx, hostsFile, onFileChange); err != nil {
			select {
			case logCh <- fmt.Sprintf("[red][%s] Watcher error: %v — auto-reload disabled, restart mping to re-enable[-]",
				time.Now().Format("15:04:05"), err):
			default:
			}
		}
	}()
	return cancelFn, innerDone
}

// startJSONWriter launches the periodic JSON snapshot writer goroutine when
// path is set. When it isn't, it still returns a valid cancel func (safe to
// call unconditionally) and a pre-closed done channel.
func startJSONWriter(path string, targets []*stats.TargetStats, httpResults func() []*stats.HTTPCheckResult, errOut io.Writer) (cancel func(), done chan struct{}) {
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
				if err := writeJSONSnapshot(path, targets, httpResults()); err != nil {
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
// rc.requestFileReload/requestHostsChange, so rc.apply() finds no pending
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
	var once sync.Once
	go func() {
		select {
		case <-durationCtx.Done():
			select {
			case logCh <- fmt.Sprintf("[green][%s] Duration limit reached, shutting down...[-]",
				time.Now().Format("15:04:05")):
			default:
			}
			sig.fire()
		case <-done:
		}
	}()
	return func() { once.Do(func() { close(done) }) }
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
	portCount     int
	sup           *supervisor
	resetMTR      func()
	resetHTTP     func()
	resetPort     func()
	thresholds    ui.Thresholds
	sig           *reloadSignal
	logCh         chan string
	rc            *reloadCoordinator
	currentHosts  []targetSpec
	currentGroups []ui.TargetGroup
}

// dscpColumnEnabled decides whether the TUI's DSCP column should show,
// following the ASN/PTR dynamic-column pattern (ui.RunOptions.DSCPEnabled):
// shown when DSCP marking is actually in play, either globally (--dscp /
// dscp:) or via any target's per-target override — matching how ASNEnabled/
// PTREnabled key off the flag that turns the underlying lookups on, rather
// than always reserving screen space for a column most runs never populate.
func dscpColumnEnabled(cfg config, hosts []targetSpec) bool {
	if cfg.dscp != "" {
		return true
	}
	for _, h := range hosts {
		if h.DSCP != "" {
			return true
		}
	}
	return false
}

// buildRunOptions assembles the ui.RunOptions for one loop iteration,
// including the OnAddHost/OnDeleteHost callbacks that arm an in-memory
// reload via the reloadCoordinator.
func buildRunOptions(p runOptionsParams) ui.RunOptions {
	return ui.RunOptions{
		Targets:         p.targets,
		Interval:        p.interval,
		Timeout:         p.timeout,
		DoneCh:          p.doneCh,
		SourceIPv4:      p.dispV4,
		SourceIPv6:      p.dispV6,
		PacketSize:      p.packetSize,
		InitialLogs:     p.preLogs,
		TraceEnabled:    p.cfg.trace,
		MTREnabled:      p.cfg.mtr,
		PortEnabled:     p.portCount > 0,
		HTTPEnabled:     len(p.cfg.httpURLs) > 0,
		HTTPResults:     p.sup.httpResults,
		ASNEnabled:      p.cfg.asnEnabled,
		PTREnabled:      p.cfg.ptrEnabled,
		DSCPEnabled:     dscpColumnEnabled(p.cfg, p.currentHosts),
		Thresholds:      &p.thresholds,
		ExternalCloseCh: p.sig.ch,
		ExternalLogCh:   p.logCh,
		OnStop:          p.sup.stopAll,
		OnRestart: func() error {
			return p.sup.do(cmdRestart)
		},
		OnResetTrace: p.sup.resetTrace,
		OnResetMTR:   p.resetMTR,
		OnResetPort:  p.resetPort,
		OnResetHTTP:  p.resetHTTP,
		OnAddHost: func(host string) error {
			host = strings.TrimSpace(host)
			if host == "" {
				return fmt.Errorf("host cannot be empty")
			}
			for _, h := range p.currentHosts {
				if h.display() == host {
					return fmt.Errorf("host %q is already in the list", host)
				}
			}
			newHosts := make([]targetSpec, len(p.currentHosts)+1)
			copy(newHosts, p.currentHosts)
			newHosts[len(p.currentHosts)] = targetSpec{Host: host}
			p.rc.requestHostsChange(p.sig, newHosts)
			return nil
		},
		OnDeleteHost: func(host string) error {
			newHosts := make([]targetSpec, 0, len(p.currentHosts))
			for _, h := range p.currentHosts {
				if h.display() != host {
					newHosts = append(newHosts, h)
				}
			}
			if len(newHosts) == len(p.currentHosts) {
				return fmt.Errorf("host %q not found", host)
			}
			if len(newHosts) == 0 {
				return fmt.Errorf("cannot delete the last host")
			}
			p.rc.requestHostsChange(p.sig, newHosts)
			return nil
		},
		Groups: p.currentGroups,
	}
}

// finishIteration performs the standard post-uiRun cleanup: stop the JSON
// writer and write a final snapshot, tear the supervisor's components down,
// stop its command loop, then stop the file watcher — in that order, joining
// each before moving on.
//
// cmdTerminate must be processed BEFORE Shutdown(): terminate is what
// actually stops the pinger and checkers, and Shutdown() ends the goroutine
// that would execute it.
func finishIteration(cfg config, targets []*stats.TargetStats, sup *supervisor, errOut io.Writer, jsonCancel func(), jsonDone chan struct{}, watchCancel func(), watchDone chan struct{}) {
	jsonCancel()
	<-jsonDone
	if cfg.jsonOutputFile != "" {
		if err := writeJSONSnapshot(cfg.jsonOutputFile, targets, sup.httpResults()); err != nil {
			fmt.Fprintf(errOut, "Warning: Final JSON snapshot write failed: %v\n", err)
		}
	}
	_ = sup.do(cmdTerminate)
	sup.Shutdown()
	watchCancel()
	<-watchDone
}
