package main

import (
	"fmt"
	"sync"

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

// pendingReload is a hosts-file reload that already passed validation: the
// merged config and the (not yet --resolve-all expanded) targets it yields.
// requestFileReload derives it from rc.cliCfg, rc.fs and the parsed document,
// all fixed by then, so apply never has to repeat that work.
type pendingReload struct {
	cfg    config
	specs  []targetSpec
	groups []ui.TargetGroup
}

// reloadCoordinator owns the pending hosts-file reload (the validated result
// of parsing the YAML doc) and applies it once the previous iteration's pinger/watcher have been
// fully stopped. Host edits from the TUI or web UI do not go through here:
// the supervisor applies them live (see live_targets.go).
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
// parses and validates the hosts file; on failure it logs to logCh and
// leaves the running instance untouched, exactly as before.
func (rc *reloadCoordinator) requestFileReload(sig *reloadSignal, hostsFile string, logCh chan<- string) {
	doc, err := parseHostsFile(hostsFile)
	if err != nil {
		postLog(logCh, fmt.Sprintf("[red][%s] Reload error: %v[-]", clock(), err))
		return
	}
	docHosts, docGroups, cfg, err := applyDocToCfg(rc.cliCfg, rc.fs, doc)
	if err == nil {
		err = validateMergedHosts(cfg, docHosts, docGroups, rc.cliHosts)
	}
	if err != nil {
		postLog(logCh, fmt.Sprintf("[red][%s] Reload validation error: %v[-]", clock(), err))
		return
	}
	specs, groups := buildHostsAndGroups(docHosts, docGroups, rc.cliHosts)
	if rc.tryLive != nil {
		expanded, expandedGroups, expandErr := expandTargets(specs, groups, cfg)
		if expandErr != nil {
			reloadNotice(logCh, "Reload expansion error: "+expandErr.Error())
			return
		}
		applied, reason, err := rc.tryLive(cfg, expanded, expandedGroups)
		if err != nil {
			reloadNotice(logCh, "Reload error: "+err.Error())
			return
		}
		if applied {
			reloadNotice(logCh, "Configuration reloaded; existing target measurements retained. "+reason)
			return
		}
		reloadNotice(logCh, "Restarting measurements for configuration reload: "+reason)
	}
	rc.mu.Lock()
	rc.pending = &pendingReload{cfg: cfg, specs: specs, groups: groups}
	rc.mu.Unlock()
	sig.fire()
}

// apply consumes any pending reload request and returns the updated hosts,
// groups, and cfg, plus whether the main loop should re-enter (true) or exit
// (false), plus a non-empty TUI Log warning when the reload succeeded but
// with a caveat the user should know about (currently: --resolve-all
// re-expansion failure, TD-47). Must be called only after the previous
// iteration's pinger, port checker, HTTP checker, and watcher have all been
// stopped and joined.
func (rc *reloadCoordinator) apply(currentCfg config, currentHosts []targetSpec, currentGroups []ui.TargetGroup) ([]targetSpec, []ui.TargetGroup, config, bool, string) {
	rc.mu.Lock()
	pending := rc.pending
	rc.mu.Unlock()

	if pending == nil {
		return currentHosts, currentGroups, currentCfg, false, ""
	}

	currentHosts, currentGroups, currentCfg = pending.specs, pending.groups, pending.cfg

	var warning string
	if expandedHosts, expandedGroups, expandErr := expandTargets(currentHosts, currentGroups, currentCfg); expandErr == nil {
		currentHosts = expandedHosts
		currentGroups = expandedGroups
	} else if currentCfg.resolveAll {
		// expandTargets only does work (and can fail) when resolve-all is
		// set; otherwise it's a passthrough. Keeping the pre-expansion
		// host list here means resolve-all silently shrinks to one entry
		// per host unless the user is told (TD-47).
		warning = fmt.Sprintf("[yellow][%s] resolve-all: failed to re-expand hosts after reload (%v) — keeping previous resolution[-]",
			clock(), expandErr)
	}

	rc.mu.Lock()
	rc.pending = nil
	rc.mu.Unlock()

	return currentHosts, currentGroups, currentCfg, true, warning
}
