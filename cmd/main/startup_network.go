package main

import (
	"context"
	"net"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
)

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

func classifySourceIP(bindIP string) (bind, v4, v6 string) {
	if ip := net.ParseIP(bindIP); ip != nil && ip.To4() == nil {
		return bindIP, "", bindIP
	}
	return bindIP, bindIP, ""
}

func determineSourceIPs(cfg config, hosts []targetSpec) (string, string, string, error) {
	if cfg.sourceAddr != "" {
		bind, v4, v6 := classifySourceIP(cfg.sourceAddr)
		return bind, v4, v6, nil
	}
	if cfg.ifaceName != "" {
		ip, err := getInterfaceIP(cfg.ifaceName, cfg.ipv6Only)
		if err != nil {
			return "", "", "", err
		}
		bind, v4, v6 := classifySourceIP(ip)
		return bind, v4, v6, nil
	}

	displaySourceIPv4, displaySourceIPv6 := detectAutoSourceIPs(hosts)
	return "", displaySourceIPv4, displaySourceIPv6, nil
}

// newCustomResolver returns a *net.Resolver for --dns-server, or
// net.DefaultResolver when it's unset. bind carries the -S source address and
// -I interface name so name resolution leaves the host by the same path as
// the ICMP probes and the port/HTTP checks; it only takes effect once
// dnsServer picks a specific server to dial — the net.DefaultResolver
// fallback can't be steered this way, so bind is a no-op in that case.
func newCustomResolver(dnsServer string, bind pinger.BindConfig) *net.Resolver {
	if dnsServer == "" {
		return net.DefaultResolver
	}
	host, port, err := net.SplitHostPort(dnsServer)
	if err != nil {
		host = dnsServer
		port = "53"
	}
	dnsAddress := net.JoinHostPort(host, port)
	dialer := pinger.NewBoundDialer("udp", 2*time.Second, bind)
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "udp", dnsAddress)
		},
	}
}
