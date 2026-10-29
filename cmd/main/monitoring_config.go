package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/spf13/pflag"
)

func healthPolicy(cfg config) stats.HealthPolicy {
	p := stats.DefaultHealthPolicy()
	// Zero configs are used by library-facing tests; parsed configs always set all three.
	if cfg.healthFailures != 0 || cfg.healthRecoveries != 0 || cfg.healthWindow != 0 {
		p.Failures, p.Recoveries, p.Window = cfg.healthFailures, cfg.healthRecoveries, cfg.healthWindow
	}
	if cfg.thresholds.RTTCrit != 0 {
		p.Thresholds = cfg.thresholds
	}
	return p
}

func httpOptions(cfg config) (pinger.HTTPOptions, error) {
	for i, raw := range cfg.httpURLs {
		u, err := url.Parse(raw)
		if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return pinger.HTTPOptions{}, fmt.Errorf("http[%d]: expected http(s) URL without credentials or fragment", i)
		}
	}
	return pinger.HTTPOptions{FollowRedirects: cfg.httpFollowRedirects}, nil
}

func validateBinding(cfg config) error {
	if cfg.sourceAddr != "" {
		address, err := netip.ParseAddr(cfg.sourceAddr)
		if err != nil {
			return fmt.Errorf("source: expected an IP address")
		}
		if cfg.ipv4Only && !address.Is4() || cfg.ipv6Only && address.Is4() {
			return fmt.Errorf("source: address does not match the selected IP family")
		}
	}
	if cfg.ifaceName != "" {
		if _, err := interfaceByName(cfg.ifaceName); err != nil {
			return fmt.Errorf("interface: %w", err)
		}
	}
	return nil
}

func validateOutputPaths(cfg config) error {
	paths := []string{cfg.outputFile, cfg.jsonOutputFile, cfg.eventOutput, cfg.hostsFile}
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		canonical := canonicalOutputPath(path)
		if seen[canonical] {
			return fmt.Errorf("output and configuration files must use different paths")
		}
		seen[canonical] = true
	}
	return nil
}

func printCheckedConfig(out io.Writer, sp startupParams) error {
	overrides := []string{}
	sp.fs.Visit(func(f *pflag.Flag) { overrides = append(overrides, f.Name) })
	targets := make([]map[string]string, len(sp.hosts))
	for i, spec := range sp.hosts {
		targets[i] = map[string]string{"host": spec.Host, "address": spec.resolveAddr(), "name": spec.display()}
	}
	cfg := sp.cfg
	groups := make([]map[string]any, len(sp.groups))
	for i, group := range sp.groups {
		names := []string{}
		for _, idx := range group.Indices {
			if idx >= 0 && idx < len(sp.hosts) {
				names = append(names, sp.hosts[idx].display())
			}
		}
		groups[i] = map[string]any{"name": group.Name, "targets": names, "target_count": len(names)}
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(map[string]any{
		"valid": true, "target_count": len(targets), "targets": targets,
		"groups": groups, "cli_overrides": overrides,
		"settings": map[string]any{
			"interval_ms": cfg.intervalMs, "timeout_ms": cfg.timeoutMs,
			"size": cfg.packetSize, "count": cfg.count, "duration": cfg.duration.String(),
			"interface": cfg.ifaceName, "source": cfg.sourceAddr,
			"ipv4": cfg.ipv4Only, "ipv6": cfg.ipv6Only,
			"dns_server": cfg.dnsServer, "resolve_all": cfg.resolveAll,
			"traceroute": cfg.trace, "mtr": cfg.mtr, "asn": cfg.asnEnabled,
			"ptr": cfg.ptrEnabled, "discovery_mtu": cfg.mtuEnabled,
			"ports": cfg.portSpecs, "http_urls": cfg.httpURLs,
			"http_follow_redirects": cfg.httpFollowRedirects,
			"health_failures":       cfg.healthFailures, "health_recoveries": cfg.healthRecoveries,
			"health_window": cfg.healthWindow.String(),
			"thresholds":    thresholdsMap(cfg.thresholds),
			"web":           cfg.webEnabled, "web_port": cfg.webPort,
			"output": cfg.outputFile, "json_output": cfg.jsonOutputFile, "event_output": cfg.eventOutput,
		},
	})
}

func thresholdsMap(th stats.Thresholds) map[string]float64 {
	return map[string]float64{
		"rtt_warn_ms":    durationMs(th.RTTWarn),
		"rtt_crit_ms":    durationMs(th.RTTCrit),
		"jitter_warn_ms": durationMs(th.JitterWarn),
		"jitter_crit_ms": durationMs(th.JitterCrit),
		"loss_warn_pct":  th.LossWarn,
		"loss_crit_pct":  th.LossCrit,
	}
}
