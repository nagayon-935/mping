package main

import (
	"fmt"
	"net"
	"time"
)

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
	return selectInterfaceIP(addrs, ifaceName, wantIPv6)
}

// IPv6 address selection priority for a given interface, lower is more
// preferred. Global unicast addresses are used first, then Unique Local
// Addresses (RFC 4193), and link-local addresses only as a last resort.
const (
	ipv6RankGlobalUnicast = iota
	ipv6RankULA
	ipv6RankLinkLocal
)

// isUniqueLocalAddr reports whether ip falls within the IPv6 Unique Local
// Address range fc00::/7 (RFC 4193).
func isUniqueLocalAddr(ip net.IP) bool {
	ip16 := ip.To16()
	if ip16 == nil {
		return false
	}
	return ip16[0]&0xfe == 0xfc
}

// ipv6AddrRank classifies an IPv6 address for source-address selection.
func ipv6AddrRank(ip net.IP) int {
	switch {
	case ip.IsLinkLocalUnicast():
		return ipv6RankLinkLocal
	case isUniqueLocalAddr(ip):
		return ipv6RankULA
	default:
		return ipv6RankGlobalUnicast
	}
}

// selectInterfaceIP picks the most appropriate address of the requested
// family from addrs, the set of addresses assigned to interface ifaceName.
//
// For IPv4, it keeps the pre-existing behavior of returning the first
// non-loopback address found.
//
// For IPv6, an interface commonly carries several addresses at once
// (link-local, ULA, global unicast, RFC 4941 temporary addresses). Global
// unicast is preferred, then ULA, then link-local. Because a link-local
// address is only valid for binding when qualified with a zone (e.g.
// "fe80::1%en0"), the interface's zone is appended whenever link-local is
// the only option.
func selectInterfaceIP(addrs []net.Addr, ifaceName string, wantIPv6 bool) (string, error) {
	bestRank := -1
	var best net.IP

	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		isV4 := ipnet.IP.To4() != nil
		if wantIPv6 == isV4 {
			continue
		}
		if !wantIPv6 {
			return ipnet.IP.String(), nil
		}

		rank := ipv6AddrRank(ipnet.IP)
		if bestRank == -1 || rank < bestRank {
			bestRank = rank
			best = ipnet.IP
		}
	}

	if best != nil {
		if bestRank == ipv6RankLinkLocal {
			return best.String() + "%" + ifaceName, nil
		}
		return best.String(), nil
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

var getPreferredOutboundIPFn = getPreferredOutboundIP

// getPreferredOutboundIP determines the preferred local IP address for reaching a remote host.
func getPreferredOutboundIP(remoteAddr, network string) string {
	// network should be "udp", "udp4", or "udp6"
	dialer := &net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := dialer.Dial(network, net.JoinHostPort(remoteAddr, probePort))
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return localAddr.IP.String()
}

func hasIPv6Connectivity() bool {
	// Use Cloudflare's public IPv6 DNS address to probe for outbound IPv6 route
	out := getPreferredOutboundIPFn("2606:4700:4700::1111", "udp6")
	if out == "" {
		return false
	}
	ip := net.ParseIP(out)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return false
	}
	return true
}

func detectAutoSourceIPs(specs []targetSpec) (string, string) {
	var src4, src6 string
	for _, spec := range specs {
		cleanHost := spec.resolveAddr()
		if src4 == "" {
			if ip, err := net.ResolveIPAddr("ip4", cleanHost); err == nil && ip != nil && ip.IP != nil {
				if out := getPreferredOutboundIPFn(ip.IP.String(), "udp4"); out != "" {
					src4 = out
				}
			}
		}
		if src6 == "" {
			if ip, err := net.ResolveIPAddr("ip6", cleanHost); err == nil && ip != nil && ip.IP != nil {
				remote := ip.String()
				if out := getPreferredOutboundIPFn(remote, "udp6"); out != "" {
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
