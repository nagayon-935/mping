package main

import (
	"fmt"
	"sync"
	"time"

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
// reload and an add/delete-host reload racing).
func (s *reloadSignal) fire() {
	s.once.Do(func() { close(s.ch) })
}

// reloadCoordinator owns the YAML-reload / add-host / delete-host request
// state (run()'s former reloadMu/reloadRequested/reloadDoc/reloadNewHosts)
// and the logic to apply a pending request once the previous iteration's
// pinger/watcher have been fully stopped. TD-22③: extracted out of run() as
// a behavior-preserving move.
type reloadCoordinator struct {
	fs       *pflag.FlagSet
	cliCfg   config
	cliHosts []string

	mu        sync.Mutex
	requested bool
	doc       hostsFileYAML
	newHosts  []targetSpec // non-nil when triggered by add/delete (skips applyDocToCfg)
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
		select {
		case logCh <- fmt.Sprintf("[red][%s] Reload error: %v[-]",
			time.Now().Format("15:04:05"), err):
		default:
		}
		return
	}
	docHosts, docGroups, cfg, err := applyDocToCfg(rc.cliCfg, rc.fs, doc)
	if err == nil {
		err = validateMergedHosts(cfg, docHosts, docGroups, rc.cliHosts)
	}
	if err != nil {
		select {
		case logCh <- fmt.Sprintf("[red][%s] Reload validation error: %v[-]",
			time.Now().Format("15:04:05"), err):
		default:
		}
		return
	}
	rc.mu.Lock()
	rc.requested = true
	rc.doc = doc
	rc.newHosts = nil
	rc.mu.Unlock()
	sig.fire()
}

// requestHostsChange arms an in-memory reload (OnAddHost/OnDeleteHost),
// bypassing the YAML doc entirely.
func (rc *reloadCoordinator) requestHostsChange(sig *reloadSignal, newHosts []targetSpec) {
	rc.mu.Lock()
	rc.requested = true
	rc.newHosts = newHosts
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
	reload := rc.requested
	newHosts := rc.newHosts
	doc := rc.doc
	rc.mu.Unlock()

	if !reload {
		return currentHosts, currentGroups, currentCfg, false, ""
	}

	if newHosts != nil {
		// In-memory add/delete: use the updated host list directly.
		currentGroups = remapGroups(currentHosts, newHosts, currentGroups)
		currentHosts = newHosts
	} else {
		// File-based reload: re-apply YAML doc.
		docHosts, docGroups, newCfg, applyErr := applyDocToCfg(rc.cliCfg, rc.fs, doc)
		if applyErr != nil {
			// Shouldn't happen (validateHostsDoc passed), but be safe.
			reload = false
		} else {
			currentHosts, currentGroups = buildHostsAndGroups(docHosts, docGroups, rc.cliHosts)
			currentCfg = newCfg
		}
	}

	var warning string
	if reload {
		if expandedHosts, expandedGroups, expandErr := expandTargets(currentHosts, currentGroups, currentCfg); expandErr == nil {
			currentHosts = expandedHosts
			currentGroups = expandedGroups
		} else if currentCfg.resolveAll {
			// expandTargets only does work (and can fail) when resolve-all is
			// set; otherwise it's a passthrough. Keeping the pre-expansion
			// host list here means resolve-all silently shrinks to one entry
			// per host unless the user is told (TD-47).
			warning = fmt.Sprintf("[yellow][%s] resolve-all: failed to re-expand hosts after reload (%v) — keeping previous resolution[-]",
				time.Now().Format("15:04:05"), expandErr)
		}
	}

	rc.mu.Lock()
	rc.requested = false
	rc.doc = hostsFileYAML{}
	rc.newHosts = nil
	rc.mu.Unlock()

	return currentHosts, currentGroups, currentCfg, reload, warning
}

// Match each surviving occurrence once, so duplicate hosts and DSCP variants
// retain their original group membership when earlier entries are deleted.
func remapGroups(oldHosts, newHosts []targetSpec, groups []ui.TargetGroup) []ui.TargetGroup {
	positions := make(map[targetSpec][]int, len(newHosts))
	for i, host := range newHosts {
		positions[host] = append(positions[host], i)
	}
	oldToNew := make(map[int]int, len(oldHosts))
	for i, host := range oldHosts {
		if indices := positions[host]; len(indices) > 0 {
			oldToNew[i] = indices[0]
			positions[host] = indices[1:]
		}
	}
	var result []ui.TargetGroup
	for _, group := range groups {
		var indices []int
		for _, oldIdx := range group.Indices {
			if newIdx, ok := oldToNew[oldIdx]; ok {
				indices = append(indices, newIdx)
			}
		}
		if len(indices) > 0 {
			result = append(result, ui.TargetGroup{Name: group.Name, Indices: indices})
		}
	}
	return result
}
