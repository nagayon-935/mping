package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// resolveTimeout bounds each target DNS resolution. resolveIPAddr takes no
// context, and neither net.ResolveIPAddr nor a *net.Resolver driven with
// context.Background() has a timeout of its own — an unresponsive
// --dns-server was measured blocking ~40s. A var (not const) so tests can
// shrink it, matching asnLookupTimeout.
var resolveTimeout = 5 * time.Second

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

// Honor both the operation's cancellation and the pinger lifetime. Legacy
// resolver test doubles have no context parameter, so retain a bounded wait
// around them; production resolvers also receive the deadline directly.
func (p *Pinger) resolveIPAddrContext(ctx context.Context, network, address string) (*net.IPAddr, error) {
	if p.stopped() {
		return nil, errPingerStopped
	}
	ctx, cancel := p.lookupContext(ctx, resolveTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		if p.stopped() {
			return nil, errPingerStopped
		}
		return nil, ctx.Err()
	case <-p.done:
		return nil, errPingerStopped
	default:
	}
	if p.resolveWithContext != nil {
		addr, err := p.resolveWithContext(ctx, network, address)
		if p.stopped() {
			return nil, errPingerStopped
		}
		return addr, err
	}
	// Compatibility adapter for explicitly injected context-free resolvers.
	// Such hooks cannot be cancelled; their owner must arrange their return.
	type result struct {
		addr *net.IPAddr
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		addr, err := p.resolveIPAddr(network, address)
		ch <- result{addr, err}
	}()
	select {
	case r := <-ch:
		return r.addr, r.err
	case <-p.done:
		return nil, errPingerStopped
	case <-ctx.Done():
		if p.stopped() {
			return nil, errPingerStopped
		}
		return nil, fmt.Errorf("dns resolution for %q: %w", address, ctx.Err())
	}
}

func (p *Pinger) resolveTargetContext(ctx context.Context, t *stats.TargetStats) *net.IPAddr {
	probe := t.NewProbe()
	p.mapMu.RLock()
	address := p.resolveAddresses[t]
	p.mapMu.RUnlock()
	if address == "" {
		address = t.Host
	}
	addr, err := p.resolveIPAddrContext(ctx, "ip", address)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		// A stop is not a ping failure: recording one here would inflate
		// the loss count printed by printExitSummary on the way out.
		//
		// Everything else collapses to a generic "DNS Error" on purpose:
		// LastError is surfaced in a narrow TUI column, so the detail
		// resolveIPAddrBounded formats (which host, which timeout) is
		// deliberately dropped here rather than truncated on screen.
		if !errors.Is(err, errPingerStopped) {
			probe.OnFailure("DNS Error")
		}
		return nil
	}
	if addr == nil {
		probe.OnFailure("DNS Error")
		return nil
	}
	ipStr := addr.String()
	t.SetIP(ipStr)
	if p.AsnEnabled {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.lookupASN(t, ipStr)
		}()
	}
	if p.PtrEnabled {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.lookupPTR(t, ipStr)
		}()
	}
	return addr
}
