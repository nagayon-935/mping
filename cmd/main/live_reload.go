package main

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

// probeConfig excludes settings that do not change probe identity or
// transport. Everything else takes the full-rebuild path. Switching between
// traceroute and mtr only restarts the route engine (reloadLive); turning
// route monitoring on or off still rebuilds, since it adds or removes a pane.
func probeConfig(cfg config) config {
	cfg.trace, cfg.mtr = cfg.routeEnabled(), false
	cfg.thresholds = stats.Thresholds{}
	cfg.healthFailures, cfg.healthRecoveries, cfg.healthWindow = 0, 0, 0
	cfg.webEnabled, cfg.webPort, cfg.checkConfig = false, 0, false
	cfg.duration = 0 // startup deadline is intentionally session-wide
	return cfg
}

func diffProbeConfig(oldCfg, newCfg config) []string {
	a, b := reflect.ValueOf(probeConfig(oldCfg)), reflect.ValueOf(probeConfig(newCfg))
	var changed []string
	for i := 0; i < a.NumField(); i++ {
		if !reflect.DeepEqual(reflectValue(a.Field(i)), reflectValue(b.Field(i))) {
			changed = append(changed, settingName(a.Type().Field(i).Name))
		}
	}
	return changed
}

func (s *supervisor) reconcileTargets(cfg config, specs []targetSpec) (targets []*stats.TargetStats, added []int, reused map[*stats.TargetStats]bool) {
	type key struct{ host, ip string }
	identity := func(spec targetSpec) key { return key{spec.Host, spec.PinnedIP} }
	old := map[key][]*stats.TargetStats{}
	for i, spec := range s.cfg.specs {
		old[identity(spec)] = append(old[identity(spec)], s.cfg.targets[i])
	}
	targets = make([]*stats.TargetStats, len(specs))
	added = []int{}
	reused = map[*stats.TargetStats]bool{}
	for i, spec := range specs {
		entries := old[identity(spec)]
		if len(entries) > 0 {
			targets[i] = entries[0]
			old[identity(spec)] = entries[1:]
			reused[targets[i]] = true
		} else {
			targets[i] = s.newLiveTargets(cfg, []targetSpec{spec})[0]
			added = append(added, i)
		}
	}
	return targets, added, reused
}

func (s *supervisor) reloadLive(cfg config, specs []targetSpec, groups []ui.TargetGroup) (bool, string, error) {
	applied, reason := false, ""
	err := s.editTargets(func(s *supervisor) error {
		if changed := diffProbeConfig(s.cfg.config, cfg); len(changed) > 0 {
			reason = "probe settings changed: " + strings.Join(changed, ", ")
			return nil
		}
		targets, added, reused := s.reconcileTargets(cfg, specs)
		var dynamic dynamicPinger
		if s.state == stateRunning && (len(added) > 0 || len(reused) != len(s.cfg.targets)) {
			var ok bool
			dynamic, ok = s.livePinger()
			if !ok {
				reason = "pinger does not support live membership changes"
				return nil
			}
			var addedTargets []*stats.TargetStats
			var addedSpecs []targetSpec
			for _, i := range added {
				addedTargets = append(addedTargets, targets[i])
				addedSpecs = append(addedSpecs, specs[i])
			}
			if err := registerTargets(dynamic, addedTargets, addedSpecs); err != nil {
				return err
			}
		}
		for _, t := range s.cfg.targets {
			if reused[t] {
				continue
			}
			s.detachTargetMonitors(dynamic, t, "Target removed by configuration reload")
		}
		previous := s.cfg.config
		s.cfg.config = cfg
		// The startup-only web server keeps its actual configuration in reports.
		s.cfg.config.webEnabled, s.cfg.config.webPort = previous.webEnabled, previous.webPort
		s.cfg.config.duration = previous.duration
		for i, t := range targets {
			t.SetDisplayName(specs[i].display())
			// New targets already received this policy during construction.
			if reused[t] {
				s.configureTarget(t)
			}
		}
		s.cfg.targets, s.cfg.specs, s.cfg.groups = targets, append([]targetSpec(nil), specs...), groups
		for _, i := range added {
			t := targets[i]
			s.attachTargetMonitors(t)
			t.RecordEvent("added", "Target added by configuration reload")
		}
		// After the target list is final, so the new engine probes exactly
		// the surviving and added targets.
		if cfg.mtr != previous.mtr {
			if s.state == stateRunning {
				s.restartRoute()
			}
			reason += routeModeNotice(cfg)
		}
		applied = true
		if len(added) > 0 {
			s.clearFinished()
		}
		if cfg.duration != previous.duration {
			reason += "duration limit changes require restarting mping. "
		}
		if warning := checkWebReloadDrift(previous, cfg); warning != "" {
			reason += " " + warning
		}
		return nil
	})
	return applied, reason, err
}

// reflectValue reads primitive values and the exported fields/slices they
// contain. reflect.Value.Interface cannot read unexported config fields.
func reflectValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int64:
		return v.Int()
	case reflect.Float64:
		return v.Float()
	case reflect.Slice:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = reflectValue(v.Index(i))
		}
		return out
	case reflect.Struct:
		out := make([]any, v.NumField())
		for i := range out {
			out[i] = reflectValue(v.Field(i))
		}
		return out
	}
	return nil
}

// routeModeNotice tells the user which route mode a reload switched to.
func routeModeNotice(cfg config) string {
	if cfg.mtr {
		return "Route monitoring switched to mtr: every hop is probed each second. "
	}
	return "Route monitoring switched to traceroute: the path is rediscovered every 10 minutes. "
}

func reloadNotice(logCh chan<- string, message string) {
	postLog(logCh, fmt.Sprintf("[%s] %s", clock(), message))
}

var settingNames = map[string]string{
	"intervalMs":          "interval",
	"timeoutMs":           "timeout",
	"packetSize":          "size",
	"ifaceName":           "interface",
	"sourceAddr":          "source",
	"mtuEnabled":          "discovery-mtu",
	"trace":               "traceroute/mtr",
	"asnEnabled":          "asn",
	"ptrEnabled":          "ptr",
	"ipv4Only":            "ipv4",
	"ipv6Only":            "ipv6",
	"portSpecs":           "port",
	"httpURLs":            "http",
	"dnsServer":           "dns-server",
	"resolveAll":          "resolve-all",
	"httpFollowRedirects": "http-follow-redirects",
}

func settingName(field string) string {
	if name, ok := settingNames[field]; ok {
		return name
	}
	return field
}
