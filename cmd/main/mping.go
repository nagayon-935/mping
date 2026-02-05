package main

import (
	"bytes"
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

type pingerController interface {
	Start(privileged bool, interval, timeout time.Duration) error
	Close()
	Wait()
	DiscoverMaxPayload(dest string, start int, min int, privileged bool, logf func(string)) (int, error)
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
	conn, err := net.Dial(network, net.JoinHostPort(remoteAddr, "80"))
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
	privileged bool
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
}

type hostsFileYAML struct {
	Hosts []string `yaml:"hosts"`
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

func mergeHosts(cfg config, hosts []string) ([]string, error) {
	if cfg.hostsFile == "" {
		return hosts, nil
	}
	fileHosts, err := parseHostsFile(cfg.hostsFile)
	if err != nil {
		return nil, err
	}
	return append(fileHosts, hosts...), nil
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
	// Write CSV header
	f.Write([]byte("Timestamp,Host,IP,Seq,Status,RTT(ms),TTL,Error\n"))
	return f, nil
}

func parseArgs(args []string) (config, []string, string, error) {
	var cfg config
	var usageBuf bytes.Buffer

	fs := pflag.NewFlagSet("mping", pflag.ContinueOnError)
	fs.SetOutput(&usageBuf)

	fs.IntVarP(&cfg.intervalMs, "interval", "i", 1000, "ping interval in ms")
	fs.IntVarP(&cfg.timeoutMs, "timeout", "t", 1000, "ping timeout in ms")
	fs.BoolVarP(&cfg.privileged, "privileged", "p", true, "use privileged (raw) ICMP socket (requires sudo)")
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

	fs.Usage = func() {
		fmt.Fprintln(&usageBuf, "Usage: mping [options] host1 host2 ...")
		fmt.Fprintln(&usageBuf, "Options:")
		fs.PrintDefaults()
		fmt.Fprintln(&usageBuf, "Note: This program usually requires root privileges (sudo) for raw sockets.")
	}

	if err := fs.Parse(args); err != nil {
		return config{}, nil, usageBuf.String(), err
	}

	hosts := fs.Args()
	if len(hosts) == 0 && cfg.hostsFile == "" {
		fs.Usage()
		return config{}, nil, usageBuf.String(), fmt.Errorf("no hosts provided")
	}

	if cfg.ipv4Only && cfg.ipv6Only {
		return config{}, nil, usageBuf.String(), fmt.Errorf("cannot use both -4 and -6")
	}

	return cfg, hosts, usageBuf.String(), nil
}

func parseHostsFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Support either:
	// - YAML sequence: ["a", "b"]
	// - Mapping: {hosts: ["a", "b"]}
	var list []string
	if err := yaml.Unmarshal(data, &list); err == nil {
		if len(list) > 0 {
			return list, nil
		}
	}

	var doc hostsFileYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Hosts, nil
}

func run(args []string, out io.Writer, errOut io.Writer) int {
	cfg, hosts, usage, err := parseArgs(args)
	if err != nil {
		if err == pflag.ErrHelp {
			fmt.Fprint(out, usage)
			return 0
		}
		fmt.Fprint(errOut, usage)
		return 1
	}

	hosts, err = mergeHosts(cfg, hosts)
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

	makePinger := func(size int) pingerController {
		p := newPinger(targets, opts)
		p.SetSource(bindIP)
		p.SetSize(size)
		p.SetCount(cfg.count)
		p.SetResolveInterval(60 * time.Second)
		if logFile != nil {
			p.SetLogWriter(logFile)
		}
		return p
	}

	packetSizeToUse := cfg.packetSize
	var preLogs []string
	if cfg.mtuEnabled {
		if cfg.ipv6Only {
			fmt.Fprintln(errOut, "Warning: PMTU discovery disabled for IPv6")
		} else {
			probe := makePinger(cfg.packetSize)
			maxPayload, err := probe.DiscoverMaxPayload(hosts[0], 9872, cfg.packetSize, cfg.privileged, func(line string) {
				preLogs = append(preLogs, line)
			})
			if err != nil {
				fmt.Fprintf(errOut, "PMTU discovery failed: %v\n", err)
			} else {
				packetSizeToUse = maxPayload
			}
		}
	}

	var (
		pMu sync.Mutex
		p   pingerController
	)

	startPinger := func() error {
		next := makePinger(packetSizeToUse)
		if err := next.Start(cfg.privileged, interval, timeout); err != nil {
			return err
		}
		if cfg.trace {
			go runTraceroutes(next, targets)
		}
		pMu.Lock()
		p = next
		pMu.Unlock()
		return nil
	}

	stopPinger := func() {
		pMu.Lock()
		cur := p
		p = nil
		pMu.Unlock()
		if cur != nil {
			cur.Close()
			cur.Wait()
		}
	}

	if err := startPinger(); err != nil {
		fmt.Fprintf(errOut, "Error starting pinger: %v\n", err)
		if cfg.privileged {
			fmt.Fprintln(errOut, "Try running with sudo or use --privileged=false (UDP ping, may not support TTL on all OS).")
		}
		return 1
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
		stopPinger,
		func() error {
			stopPinger()
			return startPinger()
		},
	); err != nil {
		fmt.Fprintf(errOut, "Error running application: %v\n", err)
		stopPinger()
		return 1
	}
	stopPinger()

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

func runTraceroutes(p tracer, targets []*stats.TargetStats) {
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t *stats.TargetStats) {
			defer wg.Done()
			hops, err := p.TraceRoute(t.Host, 30, 2*time.Second)
			if err != nil {
				t.SetTraceHops([]string{"error"})
				return
			}
			t.SetTraceHops(hops)
		}(t)
	}
	wg.Wait()
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
