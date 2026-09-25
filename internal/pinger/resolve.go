package pinger

import (
	"context"
	"net"
	"net/netip"
)

// ResolveIPAddrContext resolves an address using the requested family while
// preserving IPv6 zones. Literal IPs are parsed directly (keeping any zone);
// hostnames go through LookupIP so "ip4"/"ip6" query only A/AAAA and a slow
// answer for the other family cannot stall or time out the lookup.
func ResolveIPAddrContext(ctx context.Context, resolver *net.Resolver, network, address string) (*net.IPAddr, error) {
	if network != "ip" && network != "ip4" && network != "ip6" {
		return nil, net.UnknownNetworkError(network)
	}
	if lit, err := netip.ParseAddr(address); err == nil {
		return pickIPAddr(network, address, []net.IPAddr{{IP: lit.AsSlice(), Zone: lit.Zone()}})
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIP(ctx, network, address)
	if err != nil {
		return nil, err
	}
	addrs := make([]net.IPAddr, len(ips))
	for i, ip := range ips {
		addrs[i] = net.IPAddr{IP: ip}
	}
	return pickIPAddr(network, address, addrs)
}

// pickIPAddr returns the first address matching network, preferring IPv4 for
// "ip" to match net.ResolveIPAddr.
func pickIPAddr(network, address string, addrs []net.IPAddr) (*net.IPAddr, error) {
	for _, addr := range addrs {
		if network == "ip4" && addr.IP.To4() == nil || network == "ip6" && addr.IP.To4() != nil {
			continue
		}
		if network == "ip" && addr.IP.To4() == nil {
			continue // match net.ResolveIPAddr's IPv4 preference for hostnames
		}
		return &addr, nil
	}
	if network == "ip" && len(addrs) > 0 {
		return &addrs[0], nil // IPv6-only hostname or literal, including its zone
	}
	return nil, &net.DNSError{Err: "no suitable address", Name: address, IsNotFound: true}
}
