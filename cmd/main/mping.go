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
	pmtuMaxPayload     = 9872             // max ICMP payload for 9900-byte jumbo frames (9900 - 20 IP - 8 ICMP)
	pmtuHeaderBytes    = 20 + 8           // IPv4 header (20) + ICMP header (8) subtracted from MTU to get max payload
	dnsResolveInterval = 60 * time.Second // how often each worker re-resolves the target hostname
	probePort          = "80"             // destination port used when detecting the preferred outbound IP

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
	if len(args) > 0 && args[0] == webProcessCommand {
		return runWebProcess(args[1:], os.Stdin, out)
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
	env, code, ok := prepareRunEnv(cfg, sp.hosts, out, errOut)
	if !ok {
		return code
	}
	// The web UI outlives reload iterations: it is started once here and
	// each iteration swaps its supervisor into webSrc.
	webSrc := web.NewSource()
	webSrv, ok := startWebUI(cfg, webSrc, errOut)
	if !ok {
		return 1
	}
	defer closeWebUI(webSrv, errOut)

	s := &session{
		errOut: errOut, startup: cfg,
		webSrc: webSrc, webSrv: webSrv, runUI: uiRun,
		rc:  newReloadCoordinator(sp.fs, sp.cliCfg, sp.cliHosts),
		ids: &pinger.IDAllocator{}, startedAt: time.Now().UTC(),
		cfg: cfg, hosts: sp.hosts, groups: sp.groups, env: env,
		activePortSpecsRaw: cfg.portSpecs,
	}

	// The --duration deadline bounds the whole run() invocation (an overall
	// session alarm, mirroring ping.c's -t) rather than being re-armed per
	// reload iteration — the same one-shot-from-startup treatment the web
	// server gets (a reload keeps its startup values or warns that a
	// restart is needed), unlike the source
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
