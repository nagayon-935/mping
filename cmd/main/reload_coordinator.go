package main

import (
	"fmt"
	"slices"
	"sync"

	"github.com/nagayon-935/mping/internal/hostsfile"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
)

// reloadSignal is the per-loop-iteration close signal handed to ui.Run as
// ExternalCloseCh. A fresh one is created each time run()'s main loop
// re-enters, since a closed channel can't be un-closed.
type reloadSignal struct {
	ch   chan struct{}
	once sync.Once
}

func newReloadSignal() *reloadSignal {
	return &reloadSignal{ch: make(chan struct{})}
}

// fire closes the signal channel. Safe to call more than once (e.g. a file
// reload racing --duration).
func (s *reloadSignal) fire() {
	s.once.Do(func() { close(s.ch) })
}

// pendingReload holds the validated configuration and its expanded membership.
type pendingReload struct {
	cfg    config
	specs  []targetSpec
	groups []ui.TargetGroup
}

// reloadCoordinator owns the pending hosts-file reload (the validated result
// of parsing the YAML doc) and applies it once the previous iteration's
// pinger/watcher have been fully stopped. Host edits from the TUI or web UI
// do not go through here: the supervisor applies them live (see live_targets.go).
type reloadCoordinator struct {
	fs       *pflag.FlagSet
	cliCfg   config
	cliHosts []string

	mu      sync.Mutex
	pending *pendingReload
	tryLive func(config, []targetSpec, []ui.TargetGroup) (bool, string, error)
}

func newReloadCoordinator(fs *pflag.FlagSet, cliCfg config, cliHosts []string) *reloadCoordinator {
	return &reloadCoordinator{fs: fs, cliCfg: cliCfg, cliHosts: cliHosts}
}

// requestFileReload is the watcher's onChange callback (post-debounce). It
// parses and validates the hosts file, then tries to apply it live. If a full
// rebuild is needed, it queues the validated reload and closes sig. On failure
// it logs to logCh and leaves the running instance untouched.
func (rc *reloadCoordinator) requestFileReload(sig *reloadSignal, hostsFile string, logCh chan<- string) {
	doc, err := hostsfile.Parse(hostsFile)
	if err != nil {
		postLog(logCh, logLine("red", fmt.Sprintf("Reload error: %v", err)))
		return
	}
	specs, groups, cfg, err := mergeHostsDocument(rc.cliCfg, rc.fs, doc, rc.cliHosts)
	if err != nil {
		postLog(logCh, logLine("red", fmt.Sprintf("Reload validation error: %v", err)))
		return
	}
	specs, groups = expandTargets(specs, groups, cfg)
	if rc.tryLive != nil {
		applied, reason, err := rc.tryLive(cfg, specs, groups)
		if err != nil {
			reloadNotice(logCh, "Reload error: "+err.Error())
			return
		}
		if applied {
			msg := "Configuration reloaded; existing target measurements retained."
			if reason != "" {
				msg += " " + reason
			}
			reloadNotice(logCh, msg)
			return
		}
		reloadNotice(logCh, "Restarting measurements for configuration reload: "+reason)
	}
	rc.mu.Lock()
	rc.pending = &pendingReload{cfg: cfg, specs: specs, groups: groups}
	rc.mu.Unlock()
	sig.fire()
}

// takePending transfers the accepted reload to the next iteration.
func (rc *reloadCoordinator) takePending() *pendingReload {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	pending := rc.pending
	rc.pending = nil
	return pending
}

// reloadWarnings collects notices for settings affected by a full reload.
func reloadWarnings(startup, reloaded config, activePortSpecs []string) []string {
	var warnings []string
	for _, w := range []string{
		checkWebReloadDrift(startup, reloaded),
		checkDurationReloadDrift(startup, reloaded),
		checkPortReloadDrift(activePortSpecs, reloaded.portSpecs),
	} {
		if w != "" {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

// checkPortReloadDrift explains that port checks were rebuilt from the new
// configuration. reloadWarnings emits it after a successful reload whose port
// specs differ from the ones the running checkers were built from.
func checkPortReloadDrift(activeRaw, reloadedRaw []string) string {
	if slices.Equal(activeRaw, reloadedRaw) {
		return ""
	}
	return logLine("yellow", "port: change detected in the reloaded config — port checks restarted with updated configuration")
}

func webReloadDriftText(active, reloaded config) string {
	if active.webEnabled == reloaded.webEnabled &&
		(!active.webEnabled || active.webPort == reloaded.webPort) {
		return ""
	}
	return "web: change detected in the reloaded config — the web UI requires a full restart of mping to take effect"
}

// checkWebReloadDrift warns when a reload changes web:/web-port:. The web
// server is started once from the startup config (active) and never moved;
// changing it requires restarting mping.
func checkWebReloadDrift(active, reloaded config) string {
	msg := webReloadDriftText(active, reloaded)
	if msg == "" {
		return ""
	}
	return logLine("yellow", msg)
}

func durationReloadDriftText(active, reloaded config) string {
	if active.duration == reloaded.duration {
		return ""
	}
	return "duration: change detected in the reloaded config — duration limit changes require restarting mping"
}

// checkDurationReloadDrift warns when a reload changes duration:. The session
// deadline is armed once at startup; changing it requires restarting mping.
func checkDurationReloadDrift(active, reloaded config) string {
	msg := durationReloadDriftText(active, reloaded)
	if msg == "" {
		return ""
	}
	return logLine("yellow", msg)
}
