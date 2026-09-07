package pinger

import (
	"context"
	"net"
)

// ResolveIPAddrContext resolves an address using the requested family while
// preserving IPv6 zones. LookupIPAddr supports cancellation and literal IPs.
func ResolveIPAddrContext(ctx context.Context, resolver *net.Resolver, network, address string) (*net.IPAddr, error) {
	if network != "ip" && network != "ip4" && network != "ip6" {
		return nil, net.UnknownNetworkError(network)
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, address)
	if err != nil {
		return nil, err
	}
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
