package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

const (
	pmtuMaxPayload       = 9872              // max ICMP payload for 9900-byte jumbo frames (9900 - 20 IP - 8 ICMP)
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

var newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
	return &pingerAdapter{Pinger: pinger.NewPingerWithOptions(targets, opts)}
}

var uiRun = ui.Run

func getInterfaceIP(ifaceName string, wantIPv6 bool) (string, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
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
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil {
			return 0, err
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

	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, err
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
	intervalMs int
	timeoutMs  int
	outputFile string
	hostsFile  string
	ifaceName  string
	sourceAddr string
	packetSize int
	count      int
	mtuEnabled bool
	trace      bool
	ipv4Only   bool
	ipv6Only   bool
	portSpecs  []string
}

type hostsFileYAML struct {
	Hosts      []string `yaml:"hosts"`
	IntervalMs *int     `yaml:"interval"`
	TimeoutMs  *int     `yaml:"timeout"`
	OutputFile *string  `yaml:"output"`
	IfaceName  *string  `yaml:"interface"`
	SourceAddr *string  `yaml:"source"`
	PacketSize *int     `yaml:"size"`
	Count      *int     `yaml:"count"`
	MtuEnabled *bool    `yaml:"discovery-mtu"`
	Trace      *bool    `yaml:"traceroute"`
	Ipv4Only   *bool    `yaml:"ipv4"`
	Ipv6Only   *bool    `yaml:"ipv6"`
	PortSpecs  []string `yaml:"port"`
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

func mergeHosts(cfg config, fs *pflag.FlagSet, hosts []string) ([]string, config, error) {
	if cfg.hostsFile == "" {
		return hosts, cfg, nil
	}
	doc, err := parseHostsFile(cfg.hostsFile)
	if err != nil {
		return nil, cfg, err
	}
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
	if !fs.Changed("ipv4") && doc.Ipv4Only != nil {
		cfg.ipv4Only = *doc.Ipv4Only
	}
	if !fs.Changed("ipv6") && doc.Ipv6Only != nil {
		cfg.ipv6Only = *doc.Ipv6Only
	}
	if !fs.Changed("port") && len(doc.PortSpecs) > 0 {
		cfg.portSpecs = doc.PortSpecs
	}
	if cfg.ipv4Only && cfg.ipv6Only {
		return nil, cfg, fmt.Errorf("cannot use both -4 and -6")
	}
	return append(doc.Hosts, hosts...), cfg, nil
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
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	// Write CSV header only when the file is new (empty).
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
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
	fs.StringVarP(&cfg.ifaceName, "interface", "I", "", "interface name to bind to (e.g. eth0)")
	fs.StringVarP(&cfg.sourceAddr, "source", "S", "", "source IP address to bind to")
	fs.IntVarP(&cfg.packetSize, "size", "s", 56, "packet size in bytes (payload)")
	fs.IntVarP(&cfg.count, "count", "c", 0, "stop after sending count packets")
	fs.BoolVarP(&cfg.ipv4Only, "ipv4", "4", false, "force IPv4 only")
	fs.BoolVarP(&cfg.ipv6Only, "ipv6", "6", false, "force IPv6 only")
	fs.StringSliceVarP(&cfg.portSpecs, "port", "p", nil, "port(s) to check, e.g. 443/tcp,53/udp or 443 (defaults to tcp)")

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
		return hostsFileYAML{}, err
	}

	var doc hostsFileYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return hostsFileYAML{}, err
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

func run(args []string, out io.Writer, errOut io.Writer) int {
	cfg, hosts, fs, usage, err := parseArgs(args)
	if err != nil {
		if err == pflag.ErrHelp {
			fmt.Fprint(out, usage)
			return 0
		}
		fmt.Fprint(errOut, usage)
		return 1
	}

	hosts, cfg, err = mergeHosts(cfg, fs, hosts)
	if err != nil {
		fmt.Fprintf(errOut, "Error reading hosts file: %v\n", err)
		return 1
	}
	if len(hosts) == 0 {
		fmt.Fprint(errOut, usage)
		return 1
	}

	interval := time.Duration(cfg.intervalMs) * time.Millisecond
	timeout := time.Duration(cfg.timeoutMs) * time.Millisecond

	// Determine resolution network
	resNetwork := resolveNetwork(cfg)

	// Determine source IP for binding and display
	bindIP, displaySourceIPv4, displaySourceIPv6, err := determineSourceIPs(cfg, hosts)
	if err != nil {
		fmt.Fprintf(errOut, "Error resolving interface %s: %v\n", cfg.ifaceName, err)
		return 1
	}
	if cfg.ifaceName != "" && bindIP != "" {
		fmt.Fprintf(out, "Binding to interface %s (%s)\n", cfg.ifaceName, bindIP)
	}

	// Initialize targets
	targets := initTargets(hosts)

	// Resolve settings used by all pinger instances (initial start / restart).
	opts := pinger.Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return net.ResolveIPAddr(resNetwork, address)
		},
	}

	// Determine interface MTU and set it for all targets
	ifaceMTU, err := getInterfaceMTU(cfg.ifaceName, bindIP, hosts[0])
	if err == nil {
		for _, t := range targets {
			t.SetIfaceMTU(ifaceMTU)
		}
	}

	// Setup logger if requested
	logFile, err := setupLogger(cfg.outputFile)
	if err != nil {
		fmt.Fprintf(errOut, "Error opening log file: %v\n", err)
		return 1
	}
	if logFile != nil {
		defer logFile.Close()
	}

	makePinger := makePingerFactory(targets, opts, cfg, bindIP, logFile)

	packetSizeToUse, preLogs := setupPMTU(makePinger, cfg, ifaceMTU, targets, hosts[0], errOut)

	var (
		pMu         sync.Mutex
		p           pingerController
		traceCtx    context.Context
		traceCancel context.CancelFunc
	)

	startPinger := func() error {
		next := makePinger(packetSizeToUse)
		if err := next.Start(interval, timeout); err != nil {
			return err
		}
		if cfg.trace {
			pMu.Lock()
			if traceCancel != nil {
				traceCancel()
			}
			traceCtx, traceCancel = context.WithCancel(context.Background())
			go runTraceroutes(traceCtx, next, targets)
			pMu.Unlock()
		}
		pMu.Lock()
		p = next
		pMu.Unlock()
		return nil
	}

	stopPinger := func() {
		pMu.Lock()
		if traceCancel != nil {
			traceCancel()
		}
		cur := p
		pMu.Unlock()
		if cur != nil {
			cur.Stop()
			cur.Wait()
		}
	}

	if err := startPinger(); err != nil {
		fmt.Fprintf(errOut, "Error starting pinger: %v\n", err)
		fmt.Fprintln(errOut, "This program requires root privileges (sudo) for raw ICMP sockets.")
		return 1
	}

	// Parse port specs and start port checker if any ports specified.
	var portSpecs []pinger.PortSpec
	for _, raw := range cfg.portSpecs {
		spec, err := pinger.ParsePortSpec(raw)
		if err != nil {
			fmt.Fprintf(errOut, "Invalid port spec %q: %v\n", raw, err)
			return 1
		}
		portSpecs = append(portSpecs, spec)
	}
	portChecker := setupPortChecker(targets, portSpecs, interval, timeout)

	if cfg.trace {
		// Traceroute info can be added to logs if needed
	}

	stopAll := func() {
		stopPinger()
		if portChecker != nil {
			portChecker.Stop()
		}
	}

	// resetTrace clears TraceHops and re-runs traceroute immediately.
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

	// resetPort stops and restarts the port checker for an immediate re-check.
	var resetPort func()
	if len(portSpecs) > 0 {
		resetPort = func() {
			if portChecker != nil {
				portChecker.Stop()
			}
			portChecker = setupPortChecker(targets, portSpecs, interval, timeout)
		}
	}

	// Start TUI
	if err := uiRun(
		targets,
		interval,
		nil,
		displaySourceIPv4,
		displaySourceIPv6,
		packetSizeToUse,
		preLogs,
		cfg.trace,
		len(portSpecs) > 0,
		stopAll,
		func() error {
			stopAll()
			if err := startPinger(); err != nil {
				return err
			}
			if portChecker != nil {
				portChecker = setupPortChecker(targets, portSpecs, interval, timeout)
			}
			return nil
		},
		resetTrace,
		resetPort,
	); err != nil {
		fmt.Fprintf(errOut, "Error running application: %v\n", err)
		stopAll()
		return 1
	}
	stopAll()

	// Print summary on exit
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
	return 0
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
				hops, err := p.TraceRoute(t.Host, tracerouteMaxHops, tracerouteHopTimeout)
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
