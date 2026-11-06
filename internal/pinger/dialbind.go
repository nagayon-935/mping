package pinger

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// BindConfig describes the egress path outgoing connections must take. It is
// the stream/datagram counterpart of the Source/Interface fields Pinger
// already honours for its raw ICMP sockets, so that -S and -I steer the port
// and HTTP checks down the same path as ping. A zero BindConfig means "no
// binding requested" and leaves every dial to the OS defaults.
type BindConfig struct {
	Source    string // -S: source IP address to bind outgoing sockets to
	Interface string // -I: physical interface name to bind outgoing sockets to
	SourceV4  string // Interface source when Source is empty
	SourceV6  string // Interface source when Source is empty
}

// IsZero reports whether bc requests no binding at all.
func (bc BindConfig) IsZero() bool {
	return bc.Source == "" && bc.Interface == ""
}

// localAddrFor builds the net.Dialer.LocalAddr value for network ("tcp" or
// "udp"). net.Dialer rejects a LocalAddr whose concrete type doesn't match the
// network being dialled ("mismatched local address type"), so the two cases
// cannot share one address type. Scoped IPv6 literals retain their zone.
// A source that isn't a literal IP yields nil,
// leaving the OS to pick the source address.
func localAddrFor(network, source string) net.Addr {
	address, err := netip.ParseAddr(source)
	if err != nil {
		return nil
	}
	ip := net.IP(address.AsSlice())
	if strings.HasPrefix(network, "udp") {
		return &net.UDPAddr{IP: ip, Zone: address.Zone()}
	}
	return &net.TCPAddr{IP: ip, Zone: address.Zone()}
}

// NewBoundDialer returns a net.Dialer for network ("tcp" or "udp") that
// honours bc: LocalAddr pins the source address (-S) and Control pins the
// physical egress interface (-I) via the same setsockopt the ICMP path uses.
// It is exported for callers that build their own dials - notably cmd/main's
// DNS resolver, which needs the same -S/-I treatment as the ICMP pinger and
// the port/HTTP checkers so name resolution leaves via the same egress path.
//
// Both are opt-in: with a zero BindConfig the returned dialer is a plain
// &net.Dialer{Timeout: timeout}, and on platforms without native interface binding the Control hook binds
// the matching interface source address (see bindsource*.go).
func NewBoundDialer(network string, timeout time.Duration, bc BindConfig) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	// localAddrFor returns an untyped nil net.Addr (never a typed nil pointer)
	// when there is no usable source, so this leaves LocalAddr a nil interface
	// in that case.
	d.LocalAddr = localAddrFor(network, bc.Source)
	if bc.Interface != "" {
		// Control receives the family-qualified network ("tcp4", "udp6", ...)
		// the dial actually resolved to, which is the only place the address
		// family is known — bc alone cannot tell us, since a hostname target
		// may resolve either way.
		d.Control = func(network, _ string, c syscall.RawConn) error {
			v6 := strings.HasSuffix(network, "6")
			if bc.Source == "" && !interfaceBindingSupported {
				source := bc.SourceV4
				if v6 {
					source = bc.SourceV6
				}
				if source == "" {
					return fmt.Errorf("interface %s has no source address for %s", bc.Interface, network)
				}
				return bindRawConnToSourceFn(c, source, v6)
			}
			bindRawConnToInterfaceFn(c, bc.Interface, v6)
			return nil
		}
	}
	return d
}

// newBoundTransport returns an http.Transport whose dials honour bc, or nil
// when bc requests no binding — in which case callers must leave
// http.Client.Transport unset so http.DefaultTransport stays in play.
// Cloning the default
// transport (rather than building one from scratch) preserves proxy support,
// HTTP/2 negotiation and the default connection-pool limits.
func newBoundTransport(timeout time.Duration, bc BindConfig) *http.Transport {
	if bc.IsZero() {
		return nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = NewBoundDialer("tcp", timeout, bc).DialContext
	return tr
}
