package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/nagayon-935/mping/internal/watcher"
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
	DiscoverMaxPayload(dest string, start int, min int, logf func(string)) (int, string, error)
	TraceRoute(dest string, maxHops int, timeout time.Duration) ([]string, error)
	SetSource(ip string)
	SetSize(size int)
	SetCount(count int)
	SetResolveInterval(interval time.Duration)
	SetLogWriter(w io.Writer)
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

var (
	interfaceByName = net.InterfaceByName
	netInterfaces   = net.Interfaces
)

func getInterfaceIP(ifaceName string, wantIPv6 bool) (string, error) {
	iface, err := interfaceByName(ifaceName)
	if err != nil {
		return "", fmt.Errorf("lookup interface %q: %w", ifaceName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("get addresses for interface %q: %w", ifaceName, err)
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			isV4 := ipnet.IP.To4() != nil
			if wantIPv6 && !isV4 {
				return ipnet.IP.String(), nil
			}
			if !wantIPv6 && isV4 {
				return ipnet.IP.String(), nil
			}
		}
	}
	ver := "IPv4"
	if wantIPv6 {
		ver = "IPv6"
	}
	return "", fmt.Errorf("no %s address found for interface %s", ver, ifaceName)
}

func getInterfaceMTU(ifaceName, sourceIP, firstHost string) (int, error) {
	if ifaceName != "" {
		iface, err := interfaceByName(ifaceName)
		if err != nil {
			return 0, fmt.Errorf("get interface %q: %w", ifaceName, err)
		}
		return iface.MTU, nil
	}

	lookupIP := sourceIP
	// If sourceIP is empty, we can't easily guess the outgoing interface MTU without a route lookup.
	// We'll skip complex route lookup here.
	if lookupIP == "" {
		// Fallback: Try to guess based on first host reachability
		lookupIP = getPreferredOutboundIP(firstHost, "udp")
	}
	if lookupIP == "" {
		return 0, fmt.Errorf("no interface to infer MTU from")
	}

	ifaces, err := netInterfaces()
	if err != nil {
		return 0, fmt.Errorf("list interfaces: %w", err)
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.String() == lookupIP {
					return iface.MTU, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("interface for %s not found", lookupIP)
}

// getPreferredOutboundIP determines the preferred local IP address for reaching a remote host.
func getPreferredOutboundIP(remoteAddr, network string) string {
	// network should be "udp", "udp4", or "udp6"
	conn, err := net.Dial(network, net.JoinHostPort(remoteAddr, probePort))
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func detectAutoSourceIPs(hosts []string) (string, string) {
	var src4, src6 string
	for _, host := range hosts {
		if src4 == "" {
			if ip, err := net.ResolveIPAddr("ip4", host); err == nil && ip != nil && ip.IP != nil {
				if out := getPreferredOutboundIP(ip.IP.String(), "udp4"); out != "" {
					src4 = out
				}
			}
		}
		if src6 == "" {
			if ip, err := net.ResolveIPAddr("ip6", host); err == nil && ip != nil && ip.IP != nil {
				remote := ip.String()
				if out := getPreferredOutboundIP(remote, "udp6"); out != "" {
					src6 = out
				}
			}
		}
		if src4 != "" && src6 != "" {
			break
		}
	}
	return src4, src6
}

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

	// Colour-coding / alert thresholds (warn = orange, crit = red).
	rttWarnMs    int
	rttCritMs    int
	jitterWarnMs int
	jitterCritMs int
	lossWarnPct  float64
	lossCritPct  float64
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

// thresholdsFromCfg builds a ui.Thresholds from the merged config. RTT/Jitter
// values are interpreted as milliseconds; loss values as percentages.
func thresholdsFromCfg(cfg config) ui.Thresholds {
	return ui.Thresholds{
		RTTWarn:    time.Duration(cfg.rttWarnMs) * time.Millisecond,
		RTTCrit:    time.Duration(cfg.rttCritMs) * time.Millisecond,
		JitterWarn: time.Duration(cfg.jitterWarnMs) * time.Millisecond,
		JitterCrit: time.Duration(cfg.jitterCritMs) * time.Millisecond,
		LossWarn:   cfg.lossWarnPct,
		LossCrit:   cfg.lossCritPct,
	}
}

func resolveNetwork(cfg config) string {
	if cfg.ipv4Only {
		return "ip4"
	}
	if cfg.ipv6Only {
		return "ip6"
	}
	return "ip"
}

// applyDocToCfg applies YAML document fields to cfg, respecting CLI flag
// overrides (a field is only applied when the CLI flag was not explicitly set).
// Returns the ungrouped hosts listed in the document, the raw group definitions,
// and the updated cfg.
func applyDocToCfg(cfg config, fs *pflag.FlagSet, doc hostsFileYAML) ([]string, []groupYAML, config, error) {
	if !fs.Changed("interval") && doc.IntervalMs != nil {
		cfg.intervalMs = *doc.IntervalMs
	}
	if !fs.Changed("timeout") && doc.TimeoutMs != nil {
		cfg.timeoutMs = *doc.TimeoutMs
	}
	if !fs.Changed("output") && doc.OutputFile != nil {
		cfg.outputFile = *doc.OutputFile
	}
	if !fs.Changed("interface") && doc.IfaceName != nil {
		cfg.ifaceName = *doc.IfaceName
	}
	if !fs.Changed("source") && doc.SourceAddr != nil {
		cfg.sourceAddr = *doc.SourceAddr
	}
	if !fs.Changed("size") && doc.PacketSize != nil {
		cfg.packetSize = *doc.PacketSize
	}
	if !fs.Changed("count") && doc.Count != nil {
		cfg.count = *doc.Count
	}
	if !fs.Changed("discovery-mtu") && doc.MtuEnabled != nil {
		cfg.mtuEnabled = *doc.MtuEnabled
	}
	if !fs.Changed("traceroute") && doc.Trace != nil {
		cfg.trace = *doc.Trace
	}
	if !fs.Changed("asn") && doc.AsnEnabled != nil {
		cfg.asnEnabled = *doc.AsnEnabled
	}
	if !fs.Changed("ipv4") && doc.Ipv4Only != nil {
		cfg.ipv4Only = *doc.Ipv4Only
	}
	if !fs.Changed("ipv6") && doc.Ipv6Only != nil {
		cfg.ipv6Only = *doc.Ipv6Only
	}
	if !fs.Changed("port") && len(doc.PortSpecs) > 0 {
		cfg.portSpecs = doc.PortSpecs
	}
	if !fs.Changed("http") && len(doc.HTTPURLs) > 0 {
		cfg.httpURLs = doc.HTTPURLs
	}
	if !fs.Changed("json-output") && doc.JsonOutput != nil {
		cfg.jsonOutputFile = *doc.JsonOutput
	}
	if !fs.Changed("mtr") && doc.Mtr != nil {
		cfg.mtr = *doc.Mtr
	}
	applyThresholdsDoc(&cfg, fs, doc.Thresholds)
	if cfg.ipv4Only && cfg.ipv6Only {
		return nil, nil, cfg, fmt.Errorf("cannot use both -4 and -6")
	}
	return doc.Hosts, doc.Groups, cfg, nil
}

// applyThresholdsDoc applies the YAML thresholds block to cfg, respecting CLI
// flag overrides (a value is only applied when its flag was not set).
func applyThresholdsDoc(cfg *config, fs *pflag.FlagSet, th *thresholdsYAML) {
	if th == nil {
		return
	}
	if !fs.Changed("rtt-warn") && th.RTTWarn != nil {
		cfg.rttWarnMs = *th.RTTWarn
	}
	if !fs.Changed("rtt-crit") && th.RTTCrit != nil {
		cfg.rttCritMs = *th.RTTCrit
	}
	if !fs.Changed("jitter-warn") && th.JitterWarn != nil {
		cfg.jitterWarnMs = *th.JitterWarn
	}
	if !fs.Changed("jitter-crit") && th.JitterCrit != nil {
		cfg.jitterCritMs = *th.JitterCrit
	}
	if !fs.Changed("loss-warn") && th.LossWarn != nil {
		cfg.lossWarnPct = *th.LossWarn
	}
	if !fs.Changed("loss-crit") && th.LossCrit != nil {
		cfg.lossCritPct = *th.LossCrit
	}
}

// mergeHosts merges a hosts-file's configuration into cfg and host list.
// It returns the full host list (ungrouped hosts first, then group hosts),
// the TargetGroup slice for grouped display, and the merged config.
func mergeHosts(cfg config, fs *pflag.FlagSet, hosts []string) ([]string, []ui.TargetGroup, config, error) {
	if cfg.hostsFile == "" {
		return hosts, nil, cfg, nil
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
func buildHostsAndGroups(docHosts []string, docGroups []groupYAML, cliHosts []string) ([]string, []ui.TargetGroup) {
	allHosts := append(append([]string(nil), docHosts...), cliHosts...)
	var uiGroups []ui.TargetGroup
	for _, g := range docGroups {
		startIdx := len(allHosts)
		allHosts = append(allHosts, g.Hosts...)
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
	if doc.Ipv4Only != nil && doc.Ipv6Only != nil && *doc.Ipv4Only && *doc.Ipv6Only {
		return fmt.Errorf("ipv4 and ipv6 cannot both be true")
	}
	if doc.Thresholds != nil {
		th := overlayThresholds(ui.DefaultThresholds(), doc.Thresholds)
		if err := th.Validate(); err != nil {
			return fmt.Errorf("thresholds: %w", err)
		}
	}
	return nil
}

// overlayThresholds returns base with any non-nil fields from the YAML block
// applied. RTT/Jitter values are milliseconds; loss values are percentages.
func overlayThresholds(base ui.Thresholds, th *thresholdsYAML) ui.Thresholds {
	if th == nil {
		return base
	}
	if th.RTTWarn != nil {
		base.RTTWarn = time.Duration(*th.RTTWarn) * time.Millisecond
	}
	if th.RTTCrit != nil {
		base.RTTCrit = time.Duration(*th.RTTCrit) * time.Millisecond
	}
	if th.JitterWarn != nil {
		base.JitterWarn = time.Duration(*th.JitterWarn) * time.Millisecond
	}
	if th.JitterCrit != nil {
		base.JitterCrit = time.Duration(*th.JitterCrit) * time.Millisecond
	}
	if th.LossWarn != nil {
		base.LossWarn = *th.LossWarn
	}
	if th.LossCrit != nil {
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

func determineSourceIPs(cfg config, hosts []string) (string, string, string, error) {
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

func initTargets(hosts []string) []*stats.TargetStats {
	targets := make([]*stats.TargetStats, 0, len(hosts))
	for _, host := range hosts {
		targets = append(targets, stats.NewTargetStats(host))
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

func parseArgs(args []string) (config, []string, *pflag.FlagSet, string, error) {
	var cfg config
	var usageBuf bytes.Buffer

	fs := pflag.NewFlagSet("mping", pflag.ContinueOnError)
	fs.SetOutput(&usageBuf)

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
	fs.BoolVarP(&cfg.ipv4Only, "ipv4", "4", false, "force IPv4 only")
	fs.BoolVarP(&cfg.ipv6Only, "ipv6", "6", false, "force IPv6 only")
	fs.StringSliceVarP(&cfg.portSpecs, "port", "p", nil, "port(s) to check, e.g. 443/tcp,53/udp or 443 (defaults to tcp)")
	fs.StringSliceVarP(&cfg.httpURLs, "http", "H", nil, "URL(s) to health-check, e.g. https://example.com/health (comma-separated or repeated)")
	fs.StringVarP(&cfg.jsonOutputFile, "json-output", "j", "", "write JSON statistics snapshot to this file (updated every 5s)")
	fs.BoolVarP(&cfg.mtr, "mtr", "M", false, "enable MTR-style per-hop monitor pane")

	// Colour-coding / alert thresholds (warn = orange, crit = red).
	fs.IntVar(&cfg.rttWarnMs, "rtt-warn", 50, "RTT warn threshold in ms (orange)")
	fs.IntVar(&cfg.rttCritMs, "rtt-crit", 200, "RTT crit threshold in ms (red)")
	fs.IntVar(&cfg.jitterWarnMs, "jitter-warn", 10, "jitter warn threshold in ms (orange)")
	fs.IntVar(&cfg.jitterCritMs, "jitter-crit", 50, "jitter crit threshold in ms (red)")
	fs.Float64Var(&cfg.lossWarnPct, "loss-warn", 20, "loss warn threshold in percent (orange)")
	fs.Float64Var(&cfg.lossCritPct, "loss-crit", 80, "loss crit threshold in percent (red)")

	fs.Usage = func() {
		fmt.Fprintln(&usageBuf, "Usage: mping [options] host1 host2 ...")
		fmt.Fprintln(&usageBuf, "Options:")
		fs.PrintDefaults()
		fmt.Fprintln(&usageBuf, "Note: This program usually requires root privileges (sudo) for raw sockets.")
	}

	if err := fs.Parse(args); err != nil {
		return config{}, nil, nil, usageBuf.String(), err
	}

	hosts := fs.Args()
	if len(hosts) == 0 && cfg.hostsFile == "" {
		fs.Usage()
		return config{}, nil, nil, usageBuf.String(), fmt.Errorf("no hosts provided")
	}

	if cfg.ipv4Only && cfg.ipv6Only {
		return config{}, nil, nil, usageBuf.String(), fmt.Errorf("cannot use both -4 and -6")
	}

	return cfg, hosts, fs, usageBuf.String(), nil
}

func parseHostsFile(path string) (hostsFileYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hostsFileYAML{}, fmt.Errorf("read hosts file %q: %w", path, err)
	}

	var doc hostsFileYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return hostsFileYAML{}, fmt.Errorf("parse hosts file %q: %w", path, err)
	}
	return doc, nil
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
	maxPayload, bottleneckIP, err := probe.DiscoverMaxPayload(firstHost, startPayload, cfg.packetSize, func(line string) {
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
	// ── 1. Parse command-line arguments ──────────────────────────────────────
	cfg, hosts, fs, usage, err := parseArgs(args)
	if err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fmt.Fprint(out, usage)
			return 0
		}
		fmt.Fprint(errOut, usage)
		return 1
	}

	// Save CLI-only values so reloads can apply fresh YAML over them.
	cliCfg := cfg
	cliHosts := append([]string(nil), hosts...)

	// ── 2. Initial YAML merge ────────────────────────────────────────────────
	var currentGroups []ui.TargetGroup
	hosts, currentGroups, cfg, err = mergeHosts(cfg, fs, hosts)
	if err != nil {
		fmt.Fprintf(errOut, "Error reading hosts file: %v\n", err)
		return 1
	}
	if len(hosts) == 0 {
		fmt.Fprint(errOut, usage)
		return 1
	}
	if err := thresholdsFromCfg(cfg).Validate(); err != nil {
		fmt.Fprintf(errOut, "Invalid thresholds: %v\n", err)
		return 1
	}

	// ── 3. One-time setup ────────────────────────────────────────────────────
	resNetwork := resolveNetwork(cfg)

	bindIP, displaySourceIPv4, displaySourceIPv6, err := determineSourceIPs(cfg, hosts)
	if err != nil {
		fmt.Fprintf(errOut, "Error resolving interface %s: %v\n", cfg.ifaceName, err)
		return 1
	}
	if cfg.ifaceName != "" && bindIP != "" {
		fmt.Fprintf(out, "Binding to interface %s (%s)\n", cfg.ifaceName, bindIP)
	}

	// Setup logger once; reloads reuse the same file handle.
	logFile, err := setupLogger(cfg.outputFile)
	if err != nil {
		fmt.Fprintf(errOut, "Error opening log file: %v\n", err)
		return 1
	}
	var logWriter io.Writer
	if logFile != nil {
		defer logFile.Close()
		logWriter = logFile
	}

	// Parse port specs once; port changes require a process restart.
	var portSpecs []pinger.PortSpec
	for _, raw := range cfg.portSpecs {
		spec, err := pinger.ParsePortSpec(raw)
		if err != nil {
			fmt.Fprintf(errOut, "Invalid port spec %q: %v\n", raw, err)
			return 1
		}
		portSpecs = append(portSpecs, spec)
	}

	// ── 4. Reload state ──────────────────────────────────────────────────────
	var (
		reloadMu        sync.Mutex
		reloadRequested bool
		reloadDoc       hostsFileYAML
		reloadNewHosts  []string // non-nil when triggered by add/delete (skips applyDocToCfg)
	)

	currentCfg := cfg
	currentHosts := hosts
	// currentGroups is declared above at the mergeHosts call site.

	// targets is declared outside the loop so the exit summary can read it.
	var targets []*stats.TargetStats

	// ── 5. Main run loop (re-entered on YAML reload) ──────────────────────────
	for {
		interval := time.Duration(currentCfg.intervalMs) * time.Millisecond
		timeout := time.Duration(currentCfg.timeoutMs) * time.Millisecond

		targets = initTargets(currentHosts)

		opts := pinger.Options{
			ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
				return net.ResolveIPAddr(resNetwork, address)
			},
			AsnEnabled: currentCfg.asnEnabled,
		}

		ifaceMTU, mtuErr := getInterfaceMTU(currentCfg.ifaceName, bindIP, currentHosts[0])
		if mtuErr == nil {
			for _, t := range targets {
				t.SetIfaceMTU(ifaceMTU)
			}
		}

		makePinger := makePingerFactory(targets, opts, currentCfg, bindIP, logWriter)
		packetSizeToUse, preLogs := setupPMTU(makePinger, currentCfg, ifaceMTU, targets, currentHosts[0], errOut)

		var (
			pMu         sync.Mutex
			p           pingerController
			traceCtx    context.Context
			traceCancel context.CancelFunc
			portChecker *pinger.PortChecker
			httpChecker *pinger.HTTPChecker
			mtrEngine   *mtr.Engine
			logCh       chan string // route flap and watcher log messages → TUI Log pane
		)

		// onFlap is the shared callback for MTR route-flap events.
		// logCh must be initialised before startPinger() is called.
		onFlap := func(host, desc string) {
			select {
			case logCh <- fmt.Sprintf("[yellow][%s] Route flap %s: %s[-]",
				time.Now().Format("15:04:05"), host, desc):
			default:
			}
		}

		startPinger := func() error {
			next := makePinger(packetSizeToUse)
			if err := next.Start(interval, timeout); err != nil {
				return err
			}
			pMu.Lock()
			if currentCfg.trace {
				if traceCancel != nil {
					traceCancel()
				}
				traceCtx, traceCancel = context.WithCancel(context.Background())
				go runTraceroutes(traceCtx, next, targets)
			}
			if currentCfg.mtr {
				if mtrEngine != nil {
					mtrEngine.Stop()
				}
				pa, ok := next.(*pingerAdapter)
				if !ok {
					return fmt.Errorf("internal: unexpected pinger type %T", next)
				}
				adapter := &pingerMTRAdapter{p: pa.Pinger}
				mtrEngine = mtr.NewEngine(adapter, targets, mtr.Config{
					OnFlap: onFlap,
				})
				mtrEngine.Start()
			}
			p = next
			pMu.Unlock()
			return nil
		}

		stopPinger := func() {
			pMu.Lock()
			if traceCancel != nil {
				traceCancel()
			}
			curEngine := mtrEngine
			cur := p
			pMu.Unlock()
			if curEngine != nil {
				curEngine.Stop()
			}
			if cur != nil {
				cur.Stop()
				cur.Wait()
			}
		}

		// Initialize logCh before startPinger so OnFlap callbacks never write to a nil channel.
		logCh = make(chan string, 16)

		if err := startPinger(); err != nil {
			fmt.Fprintf(errOut, "Error starting pinger: %v\n", err)
			fmt.Fprintln(errOut, "This program requires root privileges (sudo) for raw ICMP sockets.")
			return 1
		}

		pMu.Lock()
		portChecker = setupPortChecker(targets, portSpecs, interval, timeout)
		httpChecker = setupHTTPChecker(currentCfg.httpURLs, interval, timeout)
		pMu.Unlock()

		// stopAll is called from multiple sites (OnStop, OnRestart, error path,
		// normal cleanup) and must be idempotent. Pinger.Stop uses a select-guarded
		// close(done) and PortChecker.Stop uses sync.Once, so both are safe to call
		// multiple times.
		stopAll := func() {
			stopPinger()
			pMu.Lock()
			curPort := portChecker
			curHTTP := httpChecker
			pMu.Unlock()
			if curPort != nil {
				curPort.Stop()
			}
			if curHTTP != nil {
				curHTTP.Stop()
			}
		}

		resetTrace := func() {
			pMu.Lock()
			cur := p
			if traceCancel != nil {
				traceCancel()
			}
			traceCtx, traceCancel = context.WithCancel(context.Background())
			go runTraceroutes(traceCtx, cur, targets)
			pMu.Unlock()
		}

		var resetMTR func()
		if currentCfg.mtr {
			resetMTR = func() {
				for _, t := range targets {
					t.Reset()
				}
				pMu.Lock()
				curEngine := mtrEngine
				if curEngine != nil {
					curEngine.Stop()
				}
				cur := p
				pa, ok := cur.(*pingerAdapter)
				if !ok {
					pMu.Unlock()
					return
				}
				adapter := &pingerMTRAdapter{p: pa.Pinger}
				mtrEngine = mtr.NewEngine(adapter, targets, mtr.Config{
					OnFlap: onFlap,
				})
				mtrEngine.Start()
				pMu.Unlock()
			}
		}

		var resetHTTP func()
		if len(currentCfg.httpURLs) > 0 {
			resetHTTP = func() {
				pMu.Lock()
				cur := httpChecker
				httpChecker = nil
				pMu.Unlock()
				if cur != nil {
					cur.Stop()
				}
				next := setupHTTPChecker(currentCfg.httpURLs, interval, timeout)
				pMu.Lock()
				httpChecker = next
				pMu.Unlock()
			}
		}

		var resetPort func()
		if len(portSpecs) > 0 {
			resetPort = func() {
				pMu.Lock()
				cur := portChecker
				portChecker = nil
				pMu.Unlock()
				if cur != nil {
					cur.Stop()
				}
				next := setupPortChecker(targets, portSpecs, interval, timeout)
				pMu.Lock()
				portChecker = next
				pMu.Unlock()
			}
		}

		// doneCh is closed when the pinger finishes (count-limited mode).
		var doneCh chan struct{}
		if currentCfg.count > 0 {
			doneCh = make(chan struct{})
			go func() {
				pMu.Lock()
				cur := p
				pMu.Unlock()
				if cur != nil {
					cur.Wait()
				}
				close(doneCh)
			}()
		}

		// reloadCh is closed by the YAML watcher to signal TUI shutdown.
		reloadCh := make(chan struct{})
		var reloadCloseOnce sync.Once

		// onFileChange is the callback invoked by the watcher after debounce.
		onFileChange := func() {
			doc, err := parseHostsFile(currentCfg.hostsFile)
			if err != nil {
				select {
				case logCh <- fmt.Sprintf("[red][%s] Reload error: %v[-]",
					time.Now().Format("15:04:05"), err):
				default:
				}
				return
			}
			if err := validateHostsDoc(doc); err != nil {
				select {
				case logCh <- fmt.Sprintf("[red][%s] Reload validation error: %v[-]",
					time.Now().Format("15:04:05"), err):
				default:
				}
				return
			}
			reloadMu.Lock()
			reloadRequested = true
			reloadDoc = doc
			reloadMu.Unlock()
			reloadCloseOnce.Do(func() { close(reloadCh) })
		}

		// Start the YAML file watcher (only when -f is specified).
		watchCancel := func() {}
		watchDone := make(chan struct{})
		close(watchDone) // pre-closed: no watcher needed
		if currentCfg.hostsFile != "" {
			innerDone := make(chan struct{})
			watchDone = innerDone
			watchCtx, cancel := context.WithCancel(context.Background())
			watchCancel = cancel
			go func() {
				defer close(innerDone)
				if err := watcher.Watch(watchCtx, currentCfg.hostsFile, onFileChange); err != nil {
					select {
					case logCh <- fmt.Sprintf("[red][%s] Watcher error: %v[-]",
						time.Now().Format("15:04:05"), err):
					default:
					}
				}
			}()
		}

		// Start the JSON periodic writer (only when -j is specified).
		jsonCtx, jsonCancel := context.WithCancel(context.Background())
		jsonDone := make(chan struct{})
		if currentCfg.jsonOutputFile != "" {
			go func() {
				defer close(jsonDone)
				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if err := writeJSONSnapshot(currentCfg.jsonOutputFile, targets, func() []*stats.HTTPCheckResult {
							pMu.Lock()
							defer pMu.Unlock()
							if httpChecker == nil {
								return nil
							}
							return httpChecker.Results()
						}()); err != nil {
							fmt.Fprintf(errOut, "Warning: JSON snapshot write failed: %v\n", err)
						}
					case <-jsonCtx.Done():
						return
					}
				}
			}()
		} else {
			close(jsonDone)
		}

		// ── Run TUI ──────────────────────────────────────────────────────────
		uiThresholds := thresholdsFromCfg(currentCfg)
		if err := uiRun(ui.RunOptions{
			Targets:      targets,
			Interval:     interval,
			Timeout:      timeout,
			DoneCh:       doneCh,
			SourceIPv4:   displaySourceIPv4,
			SourceIPv6:   displaySourceIPv6,
			PacketSize:   packetSizeToUse,
			InitialLogs:  preLogs,
			TraceEnabled: currentCfg.trace,
			MTREnabled:   currentCfg.mtr,
			PortEnabled:  len(portSpecs) > 0,
			HTTPEnabled:  len(currentCfg.httpURLs) > 0,
			HTTPResults: func() []*stats.HTTPCheckResult {
				pMu.Lock()
				defer pMu.Unlock()
				if httpChecker == nil {
					return nil
				}
				return httpChecker.Results()
			}(),
			ASNEnabled:      currentCfg.asnEnabled,
			Thresholds:      &uiThresholds,
			ExternalCloseCh: reloadCh,
			ExternalLogCh:   logCh,
			OnStop:          stopAll,
			OnRestart: func() error {
				stopAll()
				if err := startPinger(); err != nil {
					return err
				}
				pMu.Lock()
				portChecker = setupPortChecker(targets, portSpecs, interval, timeout)
				httpChecker = setupHTTPChecker(currentCfg.httpURLs, interval, timeout)
				pMu.Unlock()
				return nil
			},
			OnResetTrace: resetTrace,
			OnResetMTR:   resetMTR,
			OnResetPort:  resetPort,
			OnResetHTTP:  resetHTTP,
			OnAddHost: func(host string) error {
				host = strings.TrimSpace(host)
				if host == "" {
					return fmt.Errorf("host cannot be empty")
				}
				for _, h := range currentHosts {
					if h == host {
						return fmt.Errorf("host %q is already in the list", host)
					}
				}
				newHosts := make([]string, len(currentHosts)+1)
				copy(newHosts, currentHosts)
				newHosts[len(currentHosts)] = host
				reloadMu.Lock()
				reloadRequested = true
				reloadNewHosts = newHosts
				reloadMu.Unlock()
				reloadCloseOnce.Do(func() { close(reloadCh) })
				return nil
			},
			OnDeleteHost: func(host string) error {
				newHosts := make([]string, 0, len(currentHosts))
				for _, h := range currentHosts {
					if h != host {
						newHosts = append(newHosts, h)
					}
				}
				if len(newHosts) == len(currentHosts) {
					return fmt.Errorf("host %q not found", host)
				}
				if len(newHosts) == 0 {
					return fmt.Errorf("cannot delete the last host")
				}
				reloadMu.Lock()
				reloadRequested = true
				reloadNewHosts = newHosts
				reloadMu.Unlock()
				reloadCloseOnce.Do(func() { close(reloadCh) })
				return nil
			},
			Groups: currentGroups,
		}); err != nil {
			fmt.Fprintf(errOut, "Error running application: %v\n", err)
			jsonCancel()
			<-jsonDone
			watchCancel()
			stopAll()
			return 1
		}

		// ── Cleanup after TUI exits ───────────────────────────────────────────
		// 1. Stop JSON writer, then write final snapshot once the goroutine has
		//    exited to avoid concurrent writes to the same .tmp file.
		jsonCancel()
		<-jsonDone
		if currentCfg.jsonOutputFile != "" {
			if err := writeJSONSnapshot(currentCfg.jsonOutputFile, targets, func() []*stats.HTTPCheckResult {
				pMu.Lock()
				defer pMu.Unlock()
				if httpChecker == nil {
					return nil
				}
				return httpChecker.Results()
			}()); err != nil {
				fmt.Fprintf(errOut, "Warning: Final JSON snapshot write failed: %v\n", err)
			}
		}
		// 2. Stop pinger and port checker.
		stopAll()
		// 3. Stop file watcher and wait for the goroutine to exit before
		//    reading the shared reload state.
		watchCancel()
		<-watchDone

		// ── Check if a reload was requested ──────────────────────────────────
		reloadMu.Lock()
		reload := reloadRequested
		newHosts := reloadNewHosts
		if reload {
			if newHosts != nil {
				// In-memory add/delete: use the updated host list directly.
				currentHosts = newHosts
			} else {
				// File-based reload: re-apply YAML doc.
				docHosts, docGroups, newCfg, applyErr := applyDocToCfg(cliCfg, fs, reloadDoc)
				if applyErr != nil {
					// Shouldn't happen (validateHostsDoc passed), but be safe.
					reload = false
				} else {
					currentHosts, currentGroups = buildHostsAndGroups(docHosts, docGroups, cliHosts)
					currentCfg = newCfg
				}
			}
			reloadRequested = false
			reloadDoc = hostsFileYAML{}
			reloadNewHosts = nil
		}
		reloadMu.Unlock()

		if !reload {
			break
		}
		// Loop continues: targets are re-initialised with the new currentHosts.
	}

	printExitSummary(out, targets)
	return 0
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
			fmt.Fprintf(out, "rtt min/avg/max = %.3f/%.3f/%.3f ms\n",
				float64(v.MinRTT.Microseconds())/1000.0,
				float64(v.AvgRTT.Microseconds())/1000.0,
				float64(v.MaxRTT.Microseconds())/1000.0)
		}
		fmt.Fprintln(out)
	}
}

type tracer interface {
	TraceRoute(dest string, maxHops int, timeout time.Duration) ([]string, error)
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
				hops, err = p.TraceRoute(t.Host, tracerouteMaxHops, tracerouteHopTimeout)
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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
