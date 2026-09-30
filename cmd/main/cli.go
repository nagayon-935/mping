package main

import (
	"bytes"
	"fmt"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	ui "github.com/nagayon-935/mping/internal/ui"
	"github.com/spf13/pflag"
)

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
	fs.BoolVarP(&cfg.ptrEnabled, "ptr", "r", false, "lookup and display PTR (reverse DNS) records for target and hop IPs")
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
	fs.StringVar(&cfg.dscp, "dscp", "", "outbound DSCP marking: a codepoint name (EF, CS0-CS7, AF11-AF43, VA, DF) or a 0-255 TOS/TrafficClass byte; overridable per host in a hosts file via 'dscp:' (IPv6 only for per-host overrides — see docs)")

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

	if cfg.intervalMs < minIntervalMs || cfg.intervalMs > maxIntervalMs {
		return config{}, nil, nil, usageBuf.String(),
			fmt.Errorf("-i/--interval: must be %d–%d ms, got %d", minIntervalMs, maxIntervalMs, cfg.intervalMs)
	}

	if cfg.dscp != "" {
		if _, err := pinger.ParseDSCP(cfg.dscp); err != nil {
			return config{}, nil, nil, usageBuf.String(), fmt.Errorf("--dscp: %w", err)
		}
	}

	return cfg, hosts, fs, usageBuf.String(), nil
}
