package main

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

// These settings do not change a probe's identity or transport. Everything
// else deliberately takes the existing full-rebuild path.
func probeConfig(cfg config) config {
	cfg.thresholds = ui.Thresholds{}
	cfg.healthFailures, cfg.healthRecoveries, cfg.healthWindow = 0, 0, 0
	cfg.webEnabled, cfg.webPort, cfg.noTUI, cfg.checkConfig = false, 0, false, false
	cfg.eventOutput = ""
	cfg.outputFile = ""
	cfg.duration = 0 // startup deadline is intentionally session-wide
	return cfg
}

func (s *supervisor) reloadLive(cfg config, specs []targetSpec, groups []ui.TargetGroup) (bool, string, error) {
	applied, reason := false, ""
	err := s.editTargets(func(s *supervisor) error {
		if !reflect.DeepEqual(probeConfig(s.cfg.config), probeConfig(cfg)) {
			var changed []string
			a, b := reflect.ValueOf(probeConfig(s.cfg.config)), reflect.ValueOf(probeConfig(cfg))
			for i := 0; i < a.NumField(); i++ {
				if !reflect.DeepEqual(reflectValue(a.Field(i)), reflectValue(b.Field(i))) {
					changed = append(changed, settingName(a.Type().Field(i).Name))
				}
			}
			reason = "probe settings changed: " + strings.Join(changed, ", ")
			return nil
		}
		type key struct{ host, ip, dscp string }
		identity := func(spec targetSpec) key { return key{spec.Host, spec.PinnedIP, spec.DSCP} }
		old := map[key][]*stats.TargetStats{}
		for i, spec := range s.cfg.specs {
			old[identity(spec)] = append(old[identity(spec)], s.cfg.targets[i])
		}
		targets := make([]*stats.TargetStats, len(specs))
		added := []int{}
		reused := map[*stats.TargetStats]bool{}
		for i, spec := range specs {
			entries := old[identity(spec)]
			if len(entries) > 0 {
				targets[i] = entries[0]
				old[identity(spec)] = entries[1:]
				reused[targets[i]] = true
			} else {
				targets[i] = buildTargetsForIteration([]targetSpec{spec}, cfg)[0]
				s.configureTarget(targets[i])
				if len(s.cfg.targets) > 0 {
					targets[i].SetIfaceMTU(s.cfg.targets[0].GetView().IfaceMTU)
				}
				added = append(added, i)
			}
		}
		var dynamic dynamicPinger
		if s.state == stateRunning && (len(added) > 0 || len(reused) != len(s.cfg.targets)) {
			var ok bool
			dynamic, ok = s.p.(dynamicPinger)
			if !ok {
				reason = "pinger does not support live membership changes"
				return nil
			}
			registered := []*stats.TargetStats{}
			for _, i := range added {
				var dscp *int
				if specs[i].DSCP != "" {
					value, _ := pinger.ParseDSCP(specs[i].DSCP)
					dscp = &value
				}
				if err := dynamic.AddTarget(targets[i], specs[i].resolveAddr(), dscp); err != nil {
					for _, t := range registered {
						dynamic.RemoveTarget(t)
					}
					return err
				}
				registered = append(registered, targets[i])
			}
		}
		for _, t := range s.cfg.targets {
			if reused[t] {
				continue
			}
			if dynamic != nil {
				s.stopTargetTrace(t)
				if s.mtrEngine != nil {
					s.mtrEngine.RemoveTarget(t)
				}
				if s.portChecker != nil {
					s.portChecker.RemoveTarget(t)
				}
				dynamic.RemoveTarget(t)
			}
			t.RecordEvent("removed", "Target removed by configuration reload")
			s.archiveTarget(t)
		}
		previous := s.cfg.config
		s.cfg.config = cfg
		// Startup-only writers/servers keep their actual configuration in reports.
		s.cfg.config.eventOutput = previous.eventOutput
		s.cfg.config.outputFile = previous.outputFile
		s.cfg.config.webEnabled, s.cfg.config.webPort = previous.webEnabled, previous.webPort
		s.cfg.config.duration = previous.duration
		for i, t := range targets {
			t.SetDisplayName(specs[i].display())
			s.configureTarget(t)
		}
		s.cfg.targets, s.cfg.specs, s.cfg.groups = targets, append([]targetSpec(nil), specs...), groups
		for _, i := range added {
			t := targets[i]
			if s.state == stateRunning {
				if s.cfg.traceEnabled {
					s.startTargetTrace(s.p, t)
				}
				if s.mtrEngine != nil {
					s.mtrEngine.AddTarget(t)
				}
				if s.portChecker != nil {
					s.portChecker.AddTarget(t)
				}
			} else {
				t.SetMeasurementState("stopped")
			}
			t.RecordEvent("added", "Target added by configuration reload")
		}
		applied = true
		if len(added) > 0 {
			select {
			case <-s.finished:
			default:
			}
		}
		for _, change := range []struct {
			changed bool
			name    string
		}{
			{cfg.eventOutput != previous.eventOutput, "event-output"},
			{cfg.outputFile != previous.outputFile, "CSV output"},
			{cfg.duration != previous.duration, "duration limit"},
		} {
			if change.changed {
				reason += change.name + " changes require restarting mping. "
			}
		}
		if warning := checkWebReloadDrift(previous, cfg); warning != "" {
			reason += " " + warning
		}
		return nil
	})
	return applied, reason, err
}

// reflect.Value.Interface cannot read unexported config fields. This helper
// compares primitive values and the exported fields/slices they contain.
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

func reloadNotice(logCh chan<- string, message string) {
	select {
	case logCh <- fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), message):
	default:
	}
}

func settingName(field string) string {
	names := map[string]string{"intervalMs": "interval", "timeoutMs": "timeout", "packetSize": "size", "ifaceName": "interface", "sourceAddr": "source", "mtuEnabled": "discovery-mtu", "trace": "traceroute", "asnEnabled": "asn", "ptrEnabled": "ptr", "ipv4Only": "ipv4", "ipv6Only": "ipv6", "portSpecs": "port", "httpURLs": "http", "jsonOutputFile": "json-output", "dnsServer": "dns-server", "resolveAll": "resolve-all", "httpStatus": "http-status", "httpFollowRedirects": "http-follow-redirects", "httpHeaders": "http-header"}
	if name, ok := names[field]; ok {
		return name
	}
	return field
}
