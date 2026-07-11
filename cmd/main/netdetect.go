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
