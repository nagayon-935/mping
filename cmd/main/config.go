package main

import (
	"fmt"
	"time"

	"github.com/nagayon-935/mping/internal/hostsfile"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/spf13/pflag"
)

// routeEnabled reports whether the route engine runs: traceroute (-T)
// discovers the path only, mtr (-M) also probes every hop.
func (c config) routeEnabled() bool { return c.trace || c.mtr }

type config struct {
	intervalMs int
	timeoutMs  int
	hostsFile  string
	ifaceName  string
	sourceAddr string
	packetSize int
	count      int
	mtuEnabled bool
	trace      bool
	asnEnabled bool
	ptrEnabled bool
	ipv4Only   bool
	ipv6Only   bool
	portSpecs  []string
	httpURLs   []string
	mtr        bool
	dnsServer  string
	resolveAll bool
	duration   time.Duration
	// webEnabled/webPort control the loopback-only browser dashboard. Like
	// --duration they are read once at startup: a YAML reload does not
	// start, stop or move the web server.
	webEnabled bool
	webPort    int

	// CLI only and read once at startup.

	// thresholds holds the colour-coding / alert boundaries (warn = orange,
	// crit = red), already converted from the raw ms/pct flag values.
	thresholds                       stats.Thresholds
	healthFailures, healthRecoveries int
	healthWindow                     time.Duration
	checkConfig                      bool
	httpFollowRedirects              bool
}

// applyDocToCfg applies YAML document fields to cfg, respecting CLI flag
// overrides (a field is only applied when the CLI flag was not explicitly set).
// Returns the ungrouped hosts listed in the document, the raw group definitions,
// and the updated cfg.
func applyDocToCfg(cfg config, fs *pflag.FlagSet, doc hostsfile.Document) ([]hostsfile.Entry, []hostsfile.Group, config, error) {
	syncField(fs, "interval", doc.IntervalMs, &cfg.intervalMs)
	syncField(fs, "timeout", doc.TimeoutMs, &cfg.timeoutMs)
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
	syncField(fs, "mtr", doc.Mtr, &cfg.mtr)
	syncField(fs, "dns-server", doc.DNSServer, &cfg.dnsServer)
	syncField(fs, "resolve-all", doc.ResolveAll, &cfg.resolveAll)
	syncField(fs, "web", doc.Web, &cfg.webEnabled)
	syncField(fs, "web-port", doc.WebPort, &cfg.webPort)
	syncField(fs, "health-failures", doc.HealthFailures, &cfg.healthFailures)
	syncField(fs, "health-recoveries", doc.HealthRecoveries, &cfg.healthRecoveries)
	syncField(fs, "http-follow-redirects", doc.HTTPFollowRedirects, &cfg.httpFollowRedirects)
	if err := syncDuration(fs, "health-window", doc.HealthWindow, &cfg.healthWindow); err != nil {
		return nil, nil, cfg, err
	}
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
// default precedence shared by every simple (non-threshold) config field.
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

// overlayThresholdsDoc returns base with any non-nil fields from th applied.
// RTT/Jitter values are milliseconds; loss values are percentages. When fs is
// non-nil, a field is skipped if its CLI flag was explicitly set (used when
// merging a reload into the running cfg.thresholds, where CLI flags must
// keep winning). When fs is nil, every non-nil doc field is applied
// unconditionally (used by validateHostsDoc, which checks a doc's raw values
// against a baseline and has no fs to consult).
func overlayThresholdsDoc(base stats.Thresholds, fs *pflag.FlagSet, th *hostsfile.Thresholds) stats.Thresholds {
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
