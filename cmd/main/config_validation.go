package main

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/nagayon-935/mping/internal/hostsfile"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// Interval bounds shared by the YAML validator and the -i flag check, so the
// two entry points cannot drift apart. The lower bound matters beyond taste:
// the interval reaches time.NewTicker unmodified in the port and HTTP
// checkers (portchecker.go, httpchecker.go), which panics on a non-positive
// duration and would take the process down from inside a check goroutine,
// after the TUI has already taken over the terminal.
const (
	minIntervalMs = 100
	maxIntervalMs = 60000
)

// Validate the effective configuration after CLI overrides are applied. This
// is shared by initial loading and reload admission, before any probes stop.
func validateMergedHosts(cfg config, hosts []hostsfile.Entry, groups []hostsfile.Group, cliHosts []string) error {
	allHosts := append([]hostsfile.Entry(nil), hosts...)
	for _, host := range cliHosts {
		allHosts = append(allHosts, hostsfile.Entry{Host: host})
	}
	duration := cfg.duration.String()
	doc := hostsfile.Document{
		Hosts: allHosts, Groups: groups,
		IntervalMs: &cfg.intervalMs, TimeoutMs: &cfg.timeoutMs,
		PacketSize: &cfg.packetSize, Count: &cfg.count,
		Duration: &duration, Ipv4Only: &cfg.ipv4Only, Ipv6Only: &cfg.ipv6Only,
		DNSServer: &cfg.dnsServer, WebPort: &cfg.webPort,
	}
	if err := validateHostsDoc(doc); err != nil {
		return err
	}
	if cfg.trace && cfg.mtr {
		return errors.New("traceroute (-T) and mtr (-M) cannot be used together: -T shows the path only, -M also probes every hop each second")
	}
	if err := cfg.thresholds.Validate(); err != nil {
		return err
	}
	policy := healthPolicy(cfg)
	// Parsed configurations must not use the zero-config compatibility
	// defaults used by supervisor test fixtures.
	policy.Failures, policy.Recoveries, policy.Window = cfg.healthFailures, cfg.healthRecoveries, cfg.healthWindow
	if err := policy.Validate(); err != nil {
		return err
	}
	if _, err := httpOptions(cfg); err != nil {
		return err
	}
	for _, raw := range cfg.portSpecs {
		if _, err := pinger.ParsePortSpec(raw); err != nil {
			return fmt.Errorf("invalid port spec %q: %w", raw, err)
		}
	}
	return validateBinding(cfg)
}

// validateHostEntries checks the rules shared by grouped and ungrouped hosts.
// location identifies the containing list in validation errors.
func validateHostEntries(hosts []hostsfile.Entry, location string) error {
	for i, h := range hosts {
		if strings.TrimSpace(h.Host) == "" {
			return fmt.Errorf("%s[%d]: empty host entry", location, i)
		}
	}
	return nil
}

// validateHostNames rejects display names that would make two targets
// indistinguishable: a name used twice, or a name equal to another entry's
// host. A name is the target's identity in the UI and the key of the
// display→address map (buildPingerOptions), so either would make one
// target resolve to the other's address.
func validateHostNames(doc hostsfile.Document) error {
	all := append([]hostsfile.Entry(nil), doc.Hosts...)
	for _, g := range doc.Groups {
		all = append(all, g.Hosts...)
	}
	hostCount := make(map[string]int, len(all))
	for _, h := range all {
		hostCount[h.Host]++
	}
	seen := make(map[string]bool)
	for _, h := range all {
		if h.Name == "" {
			continue
		}
		if seen[h.Name] {
			return fmt.Errorf("duplicate name %q: names must be unique", h.Name)
		}
		seen[h.Name] = true
		others := hostCount[h.Name]
		if h.Host == h.Name {
			others--
		}
		if others > 0 {
			return fmt.Errorf("name %q is also used as a host by another entry", h.Name)
		}
	}
	return nil
}

// validateHostsDoc checks a hostsfile.Document for semantic errors.
// Returns a non-nil error if any field is out of range or logically invalid.
func validateHostsDoc(doc hostsfile.Document) error {
	totalHosts := len(doc.Hosts)
	for _, g := range doc.Groups {
		totalHosts += len(g.Hosts)
	}
	if totalHosts == 0 {
		return fmt.Errorf("hosts: at least one entry required (in hosts: or groups:)")
	}
	if err := validateHostEntries(doc.Hosts, "hosts"); err != nil {
		return err
	}
	for gi, g := range doc.Groups {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Errorf("groups[%d]: name is required", gi)
		}
		if len(g.Hosts) == 0 {
			return fmt.Errorf("groups[%q]: at least one host required", g.Name)
		}
		if err := validateHostEntries(g.Hosts, fmt.Sprintf("groups[%q]", g.Name)); err != nil {
			return err
		}
	}
	if err := validateHostNames(doc); err != nil {
		return err
	}
	if doc.IntervalMs != nil && (*doc.IntervalMs < minIntervalMs || *doc.IntervalMs > maxIntervalMs) {
		return fmt.Errorf("interval: must be %d–%d ms, got %d", minIntervalMs, maxIntervalMs, *doc.IntervalMs)
	}
	if doc.TimeoutMs != nil && (*doc.TimeoutMs < 10 || *doc.TimeoutMs > 30000) {
		return fmt.Errorf("timeout: must be 10–30000 ms, got %d", *doc.TimeoutMs)
	}
	if doc.PacketSize != nil && (*doc.PacketSize < 1 || *doc.PacketSize > pmtuMaxPayload) {
		return fmt.Errorf("size: must be 1–%d bytes, got %d", pmtuMaxPayload, *doc.PacketSize)
	}
	if doc.Count != nil && *doc.Count < 0 {
		return fmt.Errorf("count: must be >= 0, got %d", *doc.Count)
	}
	if doc.Duration != nil {
		if _, err := parseNonNegativeDuration(*doc.Duration); err != nil {
			return fmt.Errorf("duration: %w", err)
		}
	}
	if doc.WebPort != nil && (*doc.WebPort < 1 || *doc.WebPort > 65535) {
		return fmt.Errorf("web-port: must be 1–65535, got %d", *doc.WebPort)
	}
	if doc.Ipv4Only != nil && doc.Ipv6Only != nil && *doc.Ipv4Only && *doc.Ipv6Only {
		return fmt.Errorf("ipv4 and ipv6 cannot both be true")
	}
	if doc.DNSServer != nil && *doc.DNSServer != "" {
		host, _, err := net.SplitHostPort(*doc.DNSServer)
		if err != nil {
			host = *doc.DNSServer
		}
		if net.ParseIP(host) == nil {
			return fmt.Errorf("dns-server: invalid IP address %q", *doc.DNSServer)
		}
	}
	if doc.Thresholds != nil {
		th := overlayThresholdsDoc(stats.DefaultThresholds(), nil, doc.Thresholds)
		if err := th.Validate(); err != nil {
			return fmt.Errorf("thresholds: %w", err)
		}
	}
	return nil
}
