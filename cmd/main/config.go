package main

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
)

type config struct {
	intervalMs     int
	timeoutMs      int
	outputFile     string
	hostsFile      string
	ifaceName      string
	sourceAddr     string
	packetSize     int
	count          int
	mtuEnabled     bool
	trace          bool
	asnEnabled     bool
	ptrEnabled     bool
	ipv4Only       bool
	ipv6Only       bool
	portSpecs      []string
	httpURLs       []string
	jsonOutputFile string
	mtr            bool
	dnsServer      string
	resolveAll     bool
	duration       time.Duration
	// dscp is the raw --dscp / dscp: value (a name like "EF" or a bare
	// number), applied as the global outbound DSCP-derived TOS/TrafficClass
	// default for every target that has no per-target override (see
	// targetSpec.DSCP). Empty means "not configured" — parsed lazily by
	// buildPingerOptions rather than here, so validateHostsDoc/parseArgs can
	// reject a malformed spec before any pinger is constructed.
	dscp string

	// thresholds holds the colour-coding / alert boundaries (warn = orange,
	// crit = red), unified onto ui.Thresholds directly (TD-10) instead of
	// six separate ms/pct fields that had to be converted at every use site.
	thresholds ui.Thresholds
}

// applyDocToCfg applies YAML document fields to cfg, respecting CLI flag
// overrides (a field is only applied when the CLI flag was not explicitly set).
// Returns the ungrouped hosts listed in the document, the raw group definitions,
// and the updated cfg.
func applyDocToCfg(cfg config, fs *pflag.FlagSet, doc hostsFileYAML) ([]hostEntry, []groupYAML, config, error) {
	syncField(fs, "interval", doc.IntervalMs, &cfg.intervalMs)
	syncField(fs, "timeout", doc.TimeoutMs, &cfg.timeoutMs)
	syncField(fs, "output", doc.OutputFile, &cfg.outputFile)
	syncField(fs, "interface", doc.IfaceName, &cfg.ifaceName)
	syncField(fs, "source", doc.SourceAddr, &cfg.sourceAddr)
	syncField(fs, "size", doc.PacketSize, &cfg.packetSize)
	syncField(fs, "count", doc.Count, &cfg.count)
	syncField(fs, "discovery-mtu", doc.MtuEnabled, &cfg.mtuEnabled)
	syncField(fs, "traceroute", doc.Trace, &cfg.trace)
	syncField(fs, "asn", doc.AsnEnabled, &cfg.asnEnabled)
	syncField(fs, "ptr", doc.PtrEnabled, &cfg.ptrEnabled)
	syncField(fs, "ipv4", doc.Ipv4Only, &cfg.ipv4Only)
	syncField(fs, "ipv6", doc.Ipv6Only, &cfg.ipv6Only)
	syncSlice(fs, "port", doc.PortSpecs, &cfg.portSpecs)
	syncSlice(fs, "http", doc.HTTPURLs, &cfg.httpURLs)
	syncField(fs, "json-output", doc.JsonOutput, &cfg.jsonOutputFile)
	syncField(fs, "mtr", doc.Mtr, &cfg.mtr)
	syncField(fs, "dns-server", doc.DNSServer, &cfg.dnsServer)
	syncField(fs, "resolve-all", doc.ResolveAll, &cfg.resolveAll)
	syncField(fs, "dscp", doc.DSCP, &cfg.dscp)
	if err := syncDuration(fs, "duration", doc.Duration, &cfg.duration); err != nil {
		return nil, nil, cfg, err
	}
	cfg.thresholds = overlayThresholdsDoc(cfg.thresholds, fs, doc.Thresholds)
	if cfg.ipv4Only && cfg.ipv6Only {
		return nil, nil, cfg, fmt.Errorf("cannot use both -4 and -6")
	}
	return doc.Hosts, doc.Groups, cfg, nil
}

// syncField applies *docVal into *cfgField when non-nil and its CLI flag
// wasn't explicitly set on the command line. This is the flag > YAML >
// default precedence shared by every simple (non-threshold) config field —
// TD-19②: adding a new config field now costs one call here instead of a
// bespoke 3-line if-block.
func syncField[T any](fs *pflag.FlagSet, flag string, docVal *T, cfgField *T) {
	if fs.Changed(flag) || docVal == nil {
		return
	}
	*cfgField = *docVal
}

// syncSlice is syncField's counterpart for []string fields, which use
// emptiness rather than nil-ness to mean "not set in the doc".
func syncSlice(fs *pflag.FlagSet, flag string, docVal []string, cfgField *[]string) {
	if fs.Changed(flag) || len(docVal) == 0 {
		return
	}
	*cfgField = docVal
}

// parseNonNegativeDuration parses raw as a Go duration string (e.g. "30s",
// "5m") and rejects negative values. Shared by --duration's CLI/YAML
// validation so both surfaces reject the same malformed input the same way.
func parseNonNegativeDuration(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration must be >= 0, got %s", d)
	}
	return d, nil
}

// syncDuration is syncField's counterpart for cfg.duration: the YAML doc
// stores the raw duration string (so the file stays human-readable, e.g.
// "duration: 5m"), which must be parsed before it can overwrite cfgField.
func syncDuration(fs *pflag.FlagSet, flag string, docVal *string, cfgField *time.Duration) error {
	if fs.Changed(flag) || docVal == nil {
		return nil
	}
	d, err := parseNonNegativeDuration(*docVal)
	if err != nil {
		return fmt.Errorf("%s: %w", flag, err)
	}
	*cfgField = d
	return nil
}

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
func validateMergedHosts(cfg config, hosts []hostEntry, groups []groupYAML, cliHosts []string) error {
	allHosts := append([]hostEntry(nil), hosts...)
	for _, host := range cliHosts {
		allHosts = append(allHosts, hostEntry{Host: host})
	}
	duration := cfg.duration.String()
	doc := hostsFileYAML{
		Hosts: allHosts, Groups: groups,
		IntervalMs: &cfg.intervalMs, TimeoutMs: &cfg.timeoutMs,
		PacketSize: &cfg.packetSize, Count: &cfg.count,
		Duration: &duration, Ipv4Only: &cfg.ipv4Only, Ipv6Only: &cfg.ipv6Only,
		DNSServer: &cfg.dnsServer, DSCP: &cfg.dscp,
	}
	if err := validateHostsDoc(doc); err != nil {
		return err
	}
	return cfg.thresholds.Validate()
}

// validateHostEntries checks the rules shared by grouped and ungrouped hosts.
// location identifies the containing list in validation errors.
func validateHostEntries(hosts []hostEntry, location string) error {
	for i, h := range hosts {
		if strings.TrimSpace(h.Host) == "" {
			return fmt.Errorf("%s[%d]: empty host entry", location, i)
		}
		if h.DSCP != "" {
			if _, err := pinger.ParseDSCP(h.DSCP); err != nil {
				return fmt.Errorf("%s[%d]: dscp: %w", location, i, err)
			}
		}
	}
	return nil
}

// validateHostsDoc checks a hostsFileYAML for semantic errors.
// Returns a non-nil error if any field is out of range or logically invalid.
func validateHostsDoc(doc hostsFileYAML) error {
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
	if doc.DSCP != nil && *doc.DSCP != "" {
		if _, err := pinger.ParseDSCP(*doc.DSCP); err != nil {
			return fmt.Errorf("dscp: %w", err)
		}
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
		th := overlayThresholdsDoc(ui.DefaultThresholds(), nil, doc.Thresholds)
		if err := th.Validate(); err != nil {
			return fmt.Errorf("thresholds: %w", err)
		}
	}
	return nil
}

// overlayThresholdsDoc returns base with any non-nil fields from th applied.
// RTT/Jitter values are milliseconds; loss values are percentages. When fs is
// non-nil, a field is skipped if its CLI flag was explicitly set (used when
// merging a reload into the running cfg.thresholds, where CLI flags must
// keep winning). When fs is nil, every non-nil doc field is applied
// unconditionally (used by validateHostsDoc, which checks a doc's raw values
// against a baseline and has no fs to consult). This single function
// replaces the former applyThresholdsDoc/overlayThresholds pair (TD-10).
func overlayThresholdsDoc(base ui.Thresholds, fs *pflag.FlagSet, th *thresholdsYAML) ui.Thresholds {
	if th == nil {
		return base
	}
	changed := func(flag string) bool { return fs != nil && fs.Changed(flag) }
	if !changed("rtt-warn") && th.RTTWarn != nil {
		base.RTTWarn = time.Duration(*th.RTTWarn) * time.Millisecond
	}
	if !changed("rtt-crit") && th.RTTCrit != nil {
		base.RTTCrit = time.Duration(*th.RTTCrit) * time.Millisecond
	}
	if !changed("jitter-warn") && th.JitterWarn != nil {
		base.JitterWarn = time.Duration(*th.JitterWarn) * time.Millisecond
	}
	if !changed("jitter-crit") && th.JitterCrit != nil {
		base.JitterCrit = time.Duration(*th.JitterCrit) * time.Millisecond
	}
	if !changed("loss-warn") && th.LossWarn != nil {
		base.LossWarn = *th.LossWarn
	}
	if !changed("loss-crit") && th.LossCrit != nil {
		base.LossCrit = *th.LossCrit
	}
	return base
}
