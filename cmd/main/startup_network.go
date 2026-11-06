package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
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
	if source, err := netip.ParseAddr(cfg.sourceAddr); err == nil {
		if source.Unmap().Is4() {
			return "ip4"
		}
		return "ip6"
	}
	return "ip"
}

func classifySourceIP(bindIP string) (bind, v4, v6 string) {
	if ip, err := netip.ParseAddr(bindIP); err == nil && ip.Unmap().Is6() {
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
		if !cfg.ipv4Only && !cfg.ipv6Only {
			v4, err4 := getInterfaceIP(cfg.ifaceName, false)
			v6, err6 := getInterfaceIP(cfg.ifaceName, true)
			if err4 != nil && err6 != nil {
				return "", "", "", fmt.Errorf("no usable address on interface %s: %w", cfg.ifaceName, errors.Join(err4, err6))
			}
			// Bind the interface, leaving source/family selection to the OS
			// for each destination rather than pinning every target to IPv4.
			if len(hosts) > 0 {
				v4, v6 = detectInterfaceSourceIPs(hosts, cfg.ifaceName)
			}
			return "", v4, v6, nil
		}
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
