package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

const (
	pmtuMaxPayload       = 9872             // max ICMP payload for 9900-byte jumbo frames (9900 - 20 IP - 8 ICMP)
	pmtuHeaderBytes      = 20 + 8           // IPv4 header (20) + ICMP header (8) subtracted from MTU to get max payload
	dnsResolveInterval   = 60 * time.Second // how often each worker re-resolves the target hostname
	tracerouteInterval   = 10 * time.Minute // how often the background traceroute is re-run
	tracerouteMaxHops    = 30               // RFC 1393 recommended maximum; internet paths rarely exceed 30 hops
	tracerouteHopTimeout = 1 * time.Second  // per-hop timeout for traceroute probes
	probePort            = "80"             // destination port used when detecting the preferred outbound IP

	// exitCodeNoResponse mirrors Apple's ping.c/ping6.c: exit(nreceived == 0
	// ? 2 : 0). Returned only when --count is set (a bounded, script-friendly
	// run) and every target finished with zero replies.
	exitCodeNoResponse = 2
)

// pingerController manages the lifecycle of a pinger instance.
// Lifecycle: Start() → [running] → Stop() → Wait() → (done)
// Stop() signals the pinger to stop; Wait() blocks until all goroutines exit.
// Close() closes underlying network connections immediately (use only when
// Start() was never called or after Wait() has returned).
type pingerController interface {
	Start(interval, timeout time.Duration) error
	Stop()
	Wait()
	Close()
	DiscoverMaxPayload(ctx context.Context, dest string, start int, min int, logf func(string)) (int, string, error)
	TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error)
	SetSource(ip string)
	SetSize(size int)
	SetCount(count int)
	SetResolveInterval(interval time.Duration)
	SetLogWriter(w io.Writer)
	// MTRProber returns an mtr.HopProber view onto this controller, used to
	// wire up the MTR engine without the caller needing to downcast to a
	// concrete pinger type.
	MTRProber() mtr.HopProber
}

type pingerAdapter struct {
	*pinger.Pinger
}

func (p *pingerAdapter) SetSource(ip string) {
	p.Source = ip
}

func (p *pingerAdapter) SetSize(size int) {
	p.Size = size
}

func (p *pingerAdapter) SetCount(count int) {
	p.Count = count
}

func (p *pingerAdapter) SetResolveInterval(interval time.Duration) {
	p.ResolveInterval = interval
}

func (p *pingerAdapter) SetLogWriter(w io.Writer) {
	p.LogWriter = w
}

func (p *pingerAdapter) Close() {
	p.Pinger.Close()
}

func (p *pingerAdapter) MTRProber() mtr.HopProber {
	return &pingerMTRAdapter{p: p.Pinger}
}

// pingerMTRAdapter wraps *pinger.Pinger to satisfy mtr.HopProber.
type pingerMTRAdapter struct {
	p *pinger.Pinger
}

func (a *pingerMTRAdapter) OpenHopSocket(dest string) (mtr.HopSocket, error) {
	return a.p.OpenHopSocket(dest)
}

func (a *pingerMTRAdapter) ProbeHop(ctx context.Context, sock mtr.HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	hopSock, ok := sock.(*pinger.HopSocket)
	if !ok {
		return pinger.HopReply{}, fmt.Errorf("unexpected socket type in ProbeHop")
	}
	return a.p.ProbeHop(ctx, hopSock, dest, ttl, traceID, timeout)
}

func (a *pingerMTRAdapter) NextTraceID() int { return a.p.NextTraceID() }
func (a *pingerMTRAdapter) ASNInfoFor(ip string) pinger.ASNInfo {
	return a.p.GetASNInfoFor(ip)
}

var newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
	return &pingerAdapter{Pinger: pinger.NewPingerWithOptions(targets, opts)}
}

var uiRun = func(opts ui.RunOptions) error { return ui.Run(opts) }

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
	ipv4Only       bool
	ipv6Only       bool
	portSpecs      []string
	httpURLs       []string
	jsonOutputFile string
	mtr            bool
	dnsServer      string
	resolveAll     bool
	duration       time.Duration

	// thresholds holds the colour-coding / alert boundaries (warn = orange,
	// crit = red), unified onto ui.Thresholds directly (TD-10) instead of
	// six separate ms/pct fields that had to be converted at every use site.
	thresholds ui.Thresholds
}

// groupYAML represents a named host group in the YAML config file.
type groupYAML struct {
	Name  string   `yaml:"name"`
	Hosts []string `yaml:"hosts"`
}

type hostsFileYAML struct {
	Hosts      []string        `yaml:"hosts"`
	Groups     []groupYAML     `yaml:"groups"`
	IntervalMs *int            `yaml:"interval"`
	TimeoutMs  *int            `yaml:"timeout"`
	OutputFile *string         `yaml:"output"`
	IfaceName  *string         `yaml:"interface"`
	SourceAddr *string         `yaml:"source"`
	PacketSize *int            `yaml:"size"`
	Count      *int            `yaml:"count"`
	MtuEnabled *bool           `yaml:"discovery-mtu"`
	Trace      *bool           `yaml:"traceroute"`
	AsnEnabled *bool           `yaml:"asn"`
	Ipv4Only   *bool           `yaml:"ipv4"`
	Ipv6Only   *bool           `yaml:"ipv6"`
	PortSpecs  []string        `yaml:"port"`
	HTTPURLs   []string        `yaml:"http"`
	JsonOutput *string         `yaml:"json-output"`
	Mtr        *bool           `yaml:"mtr"`
	DNSServer  *string         `yaml:"dns-server"`
	ResolveAll *bool           `yaml:"resolve-all"`
	Duration   *string         `yaml:"duration"`
	Thresholds *thresholdsYAML `yaml:"thresholds"`
}

// thresholdsYAML mirrors the ui.Thresholds boundaries in the YAML config.
// RTT/Jitter values are milliseconds; loss values are percentages.
type thresholdsYAML struct {
	RTTWarn    *int     `yaml:"rtt-warn"`
	RTTCrit    *int     `yaml:"rtt-crit"`
	JitterWarn *int     `yaml:"jitter-warn"`
	JitterCrit *int     `yaml:"jitter-crit"`
	LossWarn   *float64 `yaml:"loss-warn"`
	LossCrit   *float64 `yaml:"loss-crit"`
}

func resolveNetwork(cfg config) string {
	if cfg.ipv4Only {
		return "ip4"
	}
	if cfg.ipv6Only {
		return "ip6"
	}
	if !hasIPv6Connectivity() {
		return "ip4"
	}
	return "ip"
}

// applyDocToCfg applies YAML document fields to cfg, respecting CLI flag
// overrides (a field is only applied when the CLI flag was not explicitly set).
// Returns the ungrouped hosts listed in the document, the raw group definitions,
// and the updated cfg.
func applyDocToCfg(cfg config, fs *pflag.FlagSet, doc hostsFileYAML) ([]string, []groupYAML, config, error) {
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
	syncField(fs, "ipv4", doc.Ipv4Only, &cfg.ipv4Only)
	syncField(fs, "ipv6", doc.Ipv6Only, &cfg.ipv6Only)
	syncSlice(fs, "port", doc.PortSpecs, &cfg.portSpecs)
	syncSlice(fs, "http", doc.HTTPURLs, &cfg.httpURLs)
	syncField(fs, "json-output", doc.JsonOutput, &cfg.jsonOutputFile)
	syncField(fs, "mtr", doc.Mtr, &cfg.mtr)
	syncField(fs, "dns-server", doc.DNSServer, &cfg.dnsServer)
	syncField(fs, "resolve-all", doc.ResolveAll, &cfg.resolveAll)
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

// mergeHosts merges a hosts-file's configuration into cfg and host list.
// It returns the full host list (ungrouped hosts first, then group hosts),
// the TargetGroup slice for grouped display, and the merged config.
func mergeHosts(cfg config, fs *pflag.FlagSet, hosts []string) ([]targetSpec, []ui.TargetGroup, config, error) {
	if cfg.hostsFile == "" {
		specs := make([]targetSpec, len(hosts))
		for i, h := range hosts {
			specs[i] = targetSpec{Host: h}
		}
		return specs, nil, cfg, nil
	}
	doc, err := parseHostsFile(cfg.hostsFile)
	if err != nil {
		return nil, nil, cfg, err
	}
	docHosts, docGroups, merged, err := applyDocToCfg(cfg, fs, doc)
	if err != nil {
		return nil, nil, merged, err
	}
	allHosts, uiGroups := buildHostsAndGroups(docHosts, docGroups, hosts)
	return allHosts, uiGroups, merged, nil
}

// buildHostsAndGroups assembles the final host list and TargetGroup slice.
// Ungrouped hosts (docHosts + cliHosts) come first; group hosts are appended
// after, with indices pointing into the combined slice.
func buildHostsAndGroups(docHosts []string, docGroups []groupYAML, cliHosts []string) ([]targetSpec, []ui.TargetGroup) {
	allHostStrs := append(append([]string(nil), docHosts...), cliHosts...)
	allHosts := make([]targetSpec, len(allHostStrs))
	for i, h := range allHostStrs {
		allHosts[i] = targetSpec{Host: h}
	}
	var uiGroups []ui.TargetGroup
	for _, g := range docGroups {
		startIdx := len(allHosts)
		for _, h := range g.Hosts {
			allHosts = append(allHosts, targetSpec{Host: h})
		}
		indices := make([]int, len(g.Hosts))
		for j := range g.Hosts {
			indices[j] = startIdx + j
		}
		uiGroups = append(uiGroups, ui.TargetGroup{Name: g.Name, Indices: indices})
	}
	return allHosts, uiGroups
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
	for i, h := range doc.Hosts {
		if strings.TrimSpace(h) == "" {
			return fmt.Errorf("hosts[%d]: empty host entry", i)
		}
	}
	for gi, g := range doc.Groups {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Errorf("groups[%d]: name is required", gi)
		}
		if len(g.Hosts) == 0 {
			return fmt.Errorf("groups[%q]: at least one host required", g.Name)
		}
		for j, h := range g.Hosts {
			if strings.TrimSpace(h) == "" {
				return fmt.Errorf("groups[%q][%d]: empty host entry", g.Name, j)
			}
		}
	}
	if doc.IntervalMs != nil && (*doc.IntervalMs < 100 || *doc.IntervalMs > 60000) {
		return fmt.Errorf("interval: must be 100–60000 ms, got %d", *doc.IntervalMs)
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

// writeJSONSnapshot serialises a statistics snapshot to path atomically.
// It writes to a temporary file first, then renames it to path, so readers
// always see a complete file.
func writeJSONSnapshot(path string, targets []*stats.TargetStats, httpResults []*stats.HTTPCheckResult) error {
	snap := stats.BuildSnapshot(targets, httpResults)
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename snapshot: %w", err)
	}
	return nil
}

func determineSourceIPs(cfg config, hosts []targetSpec) (string, string, string, error) {
	bindIP := ""
	displaySourceIPv4 := ""
	displaySourceIPv6 := ""

	if cfg.sourceAddr != "" {
		bindIP = cfg.sourceAddr
		if ip := net.ParseIP(bindIP); ip != nil && ip.To4() == nil {
			displaySourceIPv6 = bindIP
		} else {
			displaySourceIPv4 = bindIP
		}
		return bindIP, displaySourceIPv4, displaySourceIPv6, nil
	}
	if cfg.ifaceName != "" {
		ip, err := getInterfaceIP(cfg.ifaceName, cfg.ipv6Only)
		if err != nil {
			return "", "", "", err
		}
		bindIP = ip
		if parsed := net.ParseIP(bindIP); parsed != nil && parsed.To4() == nil {
			displaySourceIPv6 = bindIP
		} else {
			displaySourceIPv4 = bindIP
		}
		return bindIP, displaySourceIPv4, displaySourceIPv6, nil
	}

	displaySourceIPv4, displaySourceIPv6 = detectAutoSourceIPs(hosts)
	return bindIP, displaySourceIPv4, displaySourceIPv6, nil
}

func initTargets(specs []targetSpec) []*stats.TargetStats {
	targets := make([]*stats.TargetStats, 0, len(specs))
	for _, spec := range specs {
		targets = append(targets, stats.NewTargetStats(spec.display()))
	}
	return targets
}

func setupLogger(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	// Write CSV header only when the file is new (empty).
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat log file %q: %w", path, err)
	}
	if info.Size() == 0 {
		if _, err := f.Write([]byte("Timestamp,Host,IP,Seq,Status,RTT(ms),TTL,Error\n")); err != nil {
			f.Close()
			return nil, fmt.Errorf("write csv header: %w", err)
		}
	}
	return f, nil
}

// thresholdFlags holds the raw ms/pct values pflag fills in for the
// colour-coding / alert thresholds (warn = orange, crit = red). These are
// bound to local vars rather than cfg directly since cfg.thresholds is a
// ui.Thresholds (TD-10): the raw values are converted to ui.Thresholds once,
// right after a successful parse (see parseArgs).
type thresholdFlags struct {
	rttWarnMs, rttCritMs, jitterWarnMs, jitterCritMs int
	lossWarnPct, lossCritPct                         float64
}

// registerFlags declares every mping flag on fs, binding values into cfg and
// th. This is the single source of truth for the CLI's flag surface: both
// parseArgs (real runs) and the completion generator (cmd/main/completion.go)
// call it so the two can never drift apart.
func registerFlags(fs *pflag.FlagSet, cfg *config, th *thresholdFlags) {
	fs.IntVarP(&cfg.intervalMs, "interval", "i", 1000, "ping interval in ms")
	fs.IntVarP(&cfg.timeoutMs, "timeout", "t", 1000, "ping timeout in ms")
	fs.StringVarP(&cfg.outputFile, "output", "o", "", "log output file path (csv format)")
	fs.StringVarP(&cfg.hostsFile, "file", "f", "", "hosts list YAML file path")
	fs.BoolVarP(&cfg.mtuEnabled, "discovery-mtu", "m", false, "discover maximum payload size using DF probes (IPv4 only)")
	fs.BoolVarP(&cfg.trace, "traceroute", "T", false, "enable traceroute pane and run traceroute")
	fs.BoolVarP(&cfg.asnEnabled, "asn", "a", false, "lookup and display AS numbers for target IPs")
	fs.StringVarP(&cfg.ifaceName, "interface", "I", "", "interface name to bind to (e.g. eth0)")
	fs.StringVarP(&cfg.sourceAddr, "source", "S", "", "source IP address to bind to")
	fs.IntVarP(&cfg.packetSize, "size", "s", 56, "packet size in bytes (payload)")
	fs.IntVarP(&cfg.count, "count", "c", 0, "stop after sending count packets")
	fs.DurationVar(&cfg.duration, "duration", 0, "stop after this much time has elapsed (e.g. 30s, 5m, 1h30m); 0 disables the limit")
	fs.BoolVarP(&cfg.ipv4Only, "ipv4", "4", false, "force IPv4 only")
	fs.BoolVarP(&cfg.ipv6Only, "ipv6", "6", false, "force IPv6 only")
	fs.StringSliceVarP(&cfg.portSpecs, "port", "p", nil, "port(s) to check, e.g. 443/tcp,53/udp or 443 (defaults to tcp)")
	fs.StringSliceVarP(&cfg.httpURLs, "http", "H", nil, "URL(s) to health-check, e.g. https://example.com/health (comma-separated or repeated)")
	fs.StringVarP(&cfg.jsonOutputFile, "json-output", "j", "", "write JSON statistics snapshot to this file (updated every 5s)")
	fs.BoolVarP(&cfg.mtr, "mtr", "M", false, "enable MTR-style per-hop monitor pane")
	fs.StringVarP(&cfg.dnsServer, "dns-server", "d", "", "custom DNS server IP to use for hostname resolution")
	fs.BoolVar(&cfg.resolveAll, "resolve-all", false, "resolve target hostname to all IP addresses and monitor them concurrently")

	fs.IntVar(&th.rttWarnMs, "rtt-warn", 50, "RTT warn threshold in ms (orange)")
	fs.IntVar(&th.rttCritMs, "rtt-crit", 200, "RTT crit threshold in ms (red)")
	fs.IntVar(&th.jitterWarnMs, "jitter-warn", 10, "jitter warn threshold in ms (orange)")
	fs.IntVar(&th.jitterCritMs, "jitter-crit", 50, "jitter crit threshold in ms (red)")
	fs.Float64Var(&th.lossWarnPct, "loss-warn", 20, "loss warn threshold in percent (orange)")
	fs.Float64Var(&th.lossCritPct, "loss-crit", 80, "loss crit threshold in percent (red)")
}

func parseArgs(args []string) (config, []string, *pflag.FlagSet, string, error) {
	var cfg config
	var th thresholdFlags
	var usageBuf bytes.Buffer

	fs := pflag.NewFlagSet("mping", pflag.ContinueOnError)
	fs.SetOutput(&usageBuf)

	registerFlags(fs, &cfg, &th)

	fs.Usage = func() {
		fmt.Fprintln(&usageBuf, "Usage: mping [options] host1 host2 ...")
		fmt.Fprintln(&usageBuf, "Options:")
		fs.PrintDefaults()
		fmt.Fprintln(&usageBuf, "Note: This program usually requires root privileges (sudo) for raw sockets.")
	}

	if err := fs.Parse(args); err != nil {
		return config{}, nil, nil, usageBuf.String(), err
	}

	cfg.thresholds = ui.Thresholds{
		RTTWarn:    time.Duration(th.rttWarnMs) * time.Millisecond,
		RTTCrit:    time.Duration(th.rttCritMs) * time.Millisecond,
		JitterWarn: time.Duration(th.jitterWarnMs) * time.Millisecond,
		JitterCrit: time.Duration(th.jitterCritMs) * time.Millisecond,
		LossWarn:   th.lossWarnPct,
		LossCrit:   th.lossCritPct,
	}

	hosts := fs.Args()
	if len(hosts) == 0 && cfg.hostsFile == "" {
		fs.Usage()
		return config{}, nil, nil, usageBuf.String(), fmt.Errorf("no hosts provided")
	}

	if cfg.ipv4Only && cfg.ipv6Only {
		return config{}, nil, nil, usageBuf.String(), fmt.Errorf("cannot use both -4 and -6")
	}

	if cfg.duration < 0 {
		return config{}, nil, nil, usageBuf.String(), fmt.Errorf("--duration must be >= 0, got %s", cfg.duration)
	}

	return cfg, hosts, fs, usageBuf.String(), nil
}

func parseHostsFile(path string) (hostsFileYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hostsFileYAML{}, fmt.Errorf("read hosts file %q: %w", path, err)
	}

	var doc hostsFileYAML
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject typos/unknown keys instead of silently dropping them
	if err := dec.Decode(&doc); err != nil && err != io.EOF {
		return hostsFileYAML{}, fmt.Errorf("parse hosts file %q: %w", path, err)
	}
	return doc, nil
}

func newCustomResolver(dnsServer string) *net.Resolver {
	if dnsServer == "" {
		return net.DefaultResolver
	}
	host, port, err := net.SplitHostPort(dnsServer)
	if err != nil {
		host = dnsServer
		port = "53"
	}
	dnsAddress := net.JoinHostPort(host, port)
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: 2 * time.Second,
			}
			return d.DialContext(ctx, "udp", dnsAddress)
		},
	}
}

// makePingerFactory returns a closure that creates a configured pinger instance
// with the given payload size. The returned factory is called each time a new
// pinger is needed (initial start and after restart).
func makePingerFactory(targets []*stats.TargetStats, opts pinger.Options, cfg config, bindIP string, logFile io.Writer) func(size int) pingerController {
	return func(size int) pingerController {
		p := newPinger(targets, opts)
		p.SetSource(bindIP)
		p.SetSize(size)
		p.SetCount(cfg.count)
		p.SetResolveInterval(dnsResolveInterval)
		if logFile != nil {
			p.SetLogWriter(logFile)
		}
		return p
	}
}

// setupPMTU runs PMTU discovery using a probe pinger and updates per-target
// sizes. It returns the discovered payload size and any pre-log messages.
func setupPMTU(makePinger func(size int) pingerController, cfg config, ifaceMTU int, targets []*stats.TargetStats, firstHost string, errOut io.Writer) (packetSize int, preLogs []string) {
	packetSize = cfg.packetSize
	if !cfg.mtuEnabled {
		return
	}
	if cfg.ipv6Only {
		fmt.Fprintln(errOut, "Warning: PMTU discovery disabled for IPv6")
		return
	}
	probe := makePinger(cfg.packetSize)
	// probe.Start() is never called — DiscoverMaxPayload opens/closes
	// its own sockets internally. Stop() only closes the done channel
	// (safe to call without Start) and prevents any future goroutine
	// from blocking on it.
	defer probe.Stop()
	startPayload := pmtuMaxPayload
	if ifaceMTU > pmtuHeaderBytes {
		startPayload = ifaceMTU - pmtuHeaderBytes
	}
	maxPayload, bottleneckIP, err := probe.DiscoverMaxPayload(context.Background(), firstHost, startPayload, cfg.packetSize, func(line string) {
		preLogs = append(preLogs, line)
	})
	if err != nil {
		fmt.Fprintf(errOut, "PMTU discovery failed: %v\n", err)
		return
	}
	packetSize = maxPayload
	for _, t := range targets {
		t.SetPMTU(maxPayload)
		if bottleneckIP != "" {
			t.SetPMTUBottleneckIP(bottleneckIP)
		}
	}
	return
}

// setupPortChecker parses port specs and starts a PortChecker if any specs are
// provided. Returns nil if no specs are given.
func setupPortChecker(targets []*stats.TargetStats, portSpecs []pinger.PortSpec, interval, timeout time.Duration) *pinger.PortChecker {
	if len(portSpecs) == 0 {
		return nil
	}
	pc := pinger.NewPortChecker(targets, portSpecs, interval, timeout)
	pc.Start()
	return pc
}

func setupHTTPChecker(urls []string, interval, timeout time.Duration) *pinger.HTTPChecker {
	if len(urls) == 0 {
		return nil
	}
	hc := pinger.NewHTTPChecker(urls, interval, timeout)
	hc.Start()
	return hc
}

func run(args []string, out io.Writer, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "completion" {
		return runCompletion(args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "__complete-interfaces" {
		return runCompleteInterfaces(out)
	}

	sp, code, ok := parseAndLoadHosts(args, out, errOut)
	if !ok {
		return code
	}
	cfg, hosts, fs := sp.cfg, sp.hosts, sp.fs
	cliCfg, cliHosts := sp.cliCfg, sp.cliHosts
	currentGroups := sp.groups

	env, code, ok := prepareRunEnv(cfg, hosts, out, errOut)
	if !ok {
		return code
	}
	var logWriter io.Writer
	if env.logFile != nil {
		defer env.logFile.Close()
		logWriter = env.logFile
	}
	resNetwork, bindIP := env.resNetwork, env.bindIP
	displaySourceIPv4, displaySourceIPv6 := env.dispV4, env.dispV6
	portSpecs := env.portSpecs

	rc := newReloadCoordinator(fs, cliCfg, cliHosts)
	currentCfg := cfg
	currentHosts := hosts
	// targets is declared outside the loop so the exit summary can read it.
	var targets []*stats.TargetStats

	// durationCtx, when --duration is set, bounds the whole run() invocation
	// (an overall session alarm, mirroring ping.c's -t) rather than being
	// re-armed per reload iteration — the same one-shot-from-startup
	// treatment bindIP already gets even though ifaceName/sourceAddr are
	// re-synced into currentCfg on every reload above. It is deliberately
	// derived from the pre-loop cfg, not currentCfg, so a YAML reload cannot
	// silently extend or shorten an already-running deadline.
	var durationCtx context.Context
	if cfg.duration > 0 {
		var cancelDuration context.CancelFunc
		durationCtx, cancelDuration = context.WithTimeout(context.Background(), cfg.duration)
		defer cancelDuration()
	}

	// activePortSpecsRaw is the --port / port: value the running port
	// checker was actually built from (env.portSpecs is parsed once and
	// never re-derived on reload; see checkPortReloadDrift, TD-25).
	activePortSpecsRaw := cfg.portSpecs
	var pendingWarnings []string

	// Main run loop (re-entered on YAML reload).
	for {
		interval := time.Duration(currentCfg.intervalMs) * time.Millisecond
		timeout := time.Duration(currentCfg.timeoutMs) * time.Millisecond

		targets = buildTargetsForIteration(currentHosts, currentCfg)

		customResolver := newCustomResolver(currentCfg.dnsServer)
		opts := buildPingerOptions(currentCfg, resNetwork, customResolver, currentHosts)

		ifaceMTU, mtuErr := getInterfaceMTU(currentCfg.ifaceName, bindIP, currentHosts[0].resolveAddr())
		if mtuErr == nil {
			for _, t := range targets {
				t.SetIfaceMTU(ifaceMTU)
			}
		}

		makePinger := makePingerFactory(targets, opts, currentCfg, bindIP, logWriter)
		packetSizeToUse, preLogs := setupPMTU(makePinger, currentCfg, ifaceMTU, targets, currentHosts[0].display(), errOut)
		if len(pendingWarnings) > 0 {
			preLogs = append(preLogs, pendingWarnings...)
			pendingWarnings = nil
		}

		// logCh carries route flap and watcher log messages to the TUI Log
		// pane; it must exist before the supervisor (whose OnFlap callback
		// writes to it) is constructed.
		logCh := make(chan string, 16)

		sup := newSupervisor(supervisorConfig{
			makePinger:   makePinger,
			packetSize:   packetSizeToUse,
			targets:      targets,
			interval:     interval,
			timeout:      timeout,
			portSpecs:    portSpecs,
			httpURLs:     currentCfg.httpURLs,
			traceEnabled: currentCfg.trace,
			mtrEnabled:   currentCfg.mtr,
			logCh:        logCh,
		})

		sup.Start()

		if err := sup.startPinger(); err != nil {
			fmt.Fprintf(errOut, "Error starting pinger: %v\n", err)
			fmt.Fprintln(errOut, "This program requires root privileges (sudo) for raw ICMP sockets.")
			sup.Shutdown()
			return 1
		}

		var resetMTR, resetHTTP, resetPort func()
		if currentCfg.mtr {
			resetMTR = sup.resetMTR
		}
		if len(currentCfg.httpURLs) > 0 {
			resetHTTP = sup.resetHTTP
		}
		if len(portSpecs) > 0 {
			resetPort = sup.resetPort
		}

		// doneCh is closed when the pinger finishes (count-limited mode).
		var doneCh chan struct{}
		if currentCfg.count > 0 {
			doneCh = make(chan struct{})
			go func() {
				sup.waitPinger()
				close(doneCh)
			}()
		}

		// sig is closed to signal TUI shutdown, either by the YAML watcher, an
		// in-memory add/delete-host request, or (below) --duration elapsing.
		sig := newReloadSignal()
		onFileChange := func() { rc.requestFileReload(sig, currentCfg.hostsFile, logCh) }
		watchCancel, watchDone := startWatcher(currentCfg.hostsFile, onFileChange, logCh)
		jsonCancel, jsonDone := startJSONWriter(currentCfg.jsonOutputFile, targets, sup.httpResults, errOut)

		// stopDurationWatch converges --duration onto the same sig/
		// ExternalCloseCh path as a YAML reload: nothing here calls
		// rc.requestFileReload/requestHostsChange, so once uiRun returns,
		// rc.apply() below finds no pending reload and the loop breaks to
		// printExitSummary exactly as it would after a plain 'q' quit.
		stopDurationWatch := watchDurationLimit(durationCtx, sig, logCh)

		runOpts := buildRunOptions(runOptionsParams{
			targets: targets, interval: interval, timeout: timeout, doneCh: doneCh,
			dispV4: displaySourceIPv4, dispV6: displaySourceIPv6,
			packetSize: packetSizeToUse, preLogs: preLogs, cfg: currentCfg,
			portCount: len(portSpecs), sup: sup,
			resetMTR: resetMTR, resetHTTP: resetHTTP, resetPort: resetPort,
			thresholds: currentCfg.thresholds, sig: sig, logCh: logCh, rc: rc,
			currentHosts: currentHosts, currentGroups: currentGroups,
		})
		uiErr := uiRun(runOpts)
		stopDurationWatch()
		if uiErr != nil {
			fmt.Fprintf(errOut, "Error running application: %v\n", uiErr)
			jsonCancel()
			<-jsonDone
			watchCancel()
			_ = sup.do(cmdTerminate)
			sup.Shutdown()
			return 1
		}

		finishIteration(currentCfg, targets, sup, errOut, jsonCancel, jsonDone, watchCancel, watchDone)

		var reload bool
		var expandWarning string
		currentHosts, currentGroups, currentCfg, reload, expandWarning = rc.apply(currentCfg, currentHosts, currentGroups)
		if !reload {
			break
		}
		if portWarning := checkPortReloadDrift(activePortSpecsRaw, currentCfg.portSpecs); portWarning != "" {
			pendingWarnings = append(pendingWarnings, portWarning)
		}
		if expandWarning != "" {
			pendingWarnings = append(pendingWarnings, expandWarning)
		}
		// Loop continues: targets are re-initialised with the new currentHosts.
	}

	printExitSummary(out, targets)
	if currentCfg.count > 0 && allTargetsUnresponsive(targets) {
		return exitCodeNoResponse
	}
	return 0
}

// allTargetsUnresponsive reports whether every target finished with zero
// replies. Mirrors the nreceived == 0 check Apple's ping.c/ping6.c use to
// decide their exit code; only consulted when --count bounds the run (see
// exitCodeNoResponse) — the always-on TUI mode has no notion of "finished".
func allTargetsUnresponsive(targets []*stats.TargetStats) bool {
	for _, t := range targets {
		if t.GetView().Recv > 0 {
			return false
		}
	}
	return true
}

// printExitSummary writes the per-target ping statistics shown after the TUI exits.
func printExitSummary(out io.Writer, targets []*stats.TargetStats) {
	fmt.Fprintln(out, "\n--- mping statistics ---")
	for _, t := range targets {
		v := t.GetView()
		lossRate := 0.0
		if v.Sent > 0 {
			lossRate = (float64(v.Loss) / float64(v.Sent)) * 100
		}
		fmt.Fprintf(out, "%s (%s): %d packets transmitted, %d received, %.1f%% packet loss\n",
			v.Host, v.IP, v.Sent, v.Recv, lossRate)
		if v.Recv > 0 {
			fmt.Fprintf(out, "rtt min/avg/max/stddev = %.3f/%.3f/%.3f/%.3f ms\n",
				float64(v.MinRTT.Microseconds())/1000.0,
				float64(v.AvgRTT.Microseconds())/1000.0,
				float64(v.MaxRTT.Microseconds())/1000.0,
				float64(v.StdDev.Microseconds())/1000.0)
		}
		fmt.Fprintln(out)
	}
}

type tracer interface {
	TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error)
}

func runTraceroutes(ctx context.Context, p tracer, targets []*stats.TargetStats) {
	ticker := time.NewTicker(tracerouteInterval)
	defer ticker.Stop()

	runOnce := func() {
		for _, t := range targets {
			if len(t.GetView().TraceHops) == 0 {
				t.SetTraceHops([]string{"Tracing..."})
			}
		}

		var wg sync.WaitGroup
		for _, t := range targets {
			wg.Add(1)
			go func(t *stats.TargetStats) {
				defer wg.Done()
				var hops []string
				var err error
				hops, err = p.TraceRoute(ctx, t.Host, tracerouteMaxHops, tracerouteHopTimeout)
				if err != nil {
					t.SetTraceHops([]string{"error: " + err.Error()})
					return
				}
				if len(hops) == 0 {
					t.SetTraceHops([]string{"no route found"})
					return
				}
				t.SetTraceHops(hops)
			}(t)
		}
		wg.Wait()
	}

	runOnce() // Initial run

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func expandTargets(specs []targetSpec, groups []ui.TargetGroup, cfg config) ([]targetSpec, []ui.TargetGroup, error) {
	if !cfg.resolveAll {
		return specs, groups, nil
	}

	resolver := newCustomResolver(cfg.dnsServer)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resolvedIPs := make(map[string][]string)
	for _, spec := range specs {
		if spec.PinnedIP != "" {
			continue
		}
		rawHost := spec.Host
		if net.ParseIP(rawHost) != nil {
			resolvedIPs[spec.Host] = []string{rawHost}
			continue
		}

		network := resolveNetwork(cfg)
		ips, err := resolver.LookupIP(ctx, network, rawHost)
		if err != nil || len(ips) == 0 {
			resolvedIPs[spec.Host] = []string{rawHost}
			continue
		}

		var ipStrs []string
		for _, ip := range ips {
			ipStrs = append(ipStrs, ip.String())
		}
		resolvedIPs[spec.Host] = ipStrs
	}

	var expandedSpecs []targetSpec
	expansionMap := make(map[int][]int)

	for i, spec := range specs {
		if spec.PinnedIP != "" {
			expandedSpecs = append(expandedSpecs, spec)
			expansionMap[i] = []int{len(expandedSpecs) - 1}
			continue
		}

		ips := resolvedIPs[spec.Host]
		startIdx := len(expandedSpecs)

		for _, ip := range ips {
			if spec.Host != ip {
				expandedSpecs = append(expandedSpecs, targetSpec{Host: spec.Host, PinnedIP: ip})
			} else {
				expandedSpecs = append(expandedSpecs, targetSpec{Host: ip})
			}
		}

		endIdx := len(expandedSpecs)
		var indices []int
		for j := startIdx; j < endIdx; j++ {
			indices = append(indices, j)
		}
		expansionMap[i] = indices
	}

	var expandedGroups []ui.TargetGroup
	for _, g := range groups {
		var newIndices []int
		for _, oldIdx := range g.Indices {
			newIndices = append(newIndices, expansionMap[oldIdx]...)
		}
		expandedGroups = append(expandedGroups, ui.TargetGroup{
			Name:    g.Name,
			Indices: newIndices,
		})
	}

	return expandedSpecs, expandedGroups, nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
