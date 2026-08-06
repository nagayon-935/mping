package pinger

import (
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// BindConfig describes the egress path outgoing connections must take. It is
// the stream/datagram counterpart of the Source/Interface fields Pinger
// already honours for its raw ICMP sockets, so that -S and -I steer the port
// and HTTP checks down the same path as ping. A zero BindConfig means "no
// binding requested" and leaves every dial exactly as it was before these
// flags were wired in.
type BindConfig struct {
	Source    string // -S: source IP address to bind outgoing sockets to
	Interface string // -I: physical interface name to bind outgoing sockets to
}

// IsZero reports whether bc requests no binding at all.
func (bc BindConfig) IsZero() bool {
	return bc.Source == "" && bc.Interface == ""
}

// localAddrFor builds the net.Dialer.LocalAddr value for network ("tcp" or
// "udp"). net.Dialer rejects a LocalAddr whose concrete type doesn't match the
// network being dialled ("mismatched local address type"), so the two cases
// cannot share one address type. A source that isn't a literal IP yields nil,
// leaving the OS to pick the source address as before.
func localAddrFor(network, source string) net.Addr {
	if source == "" {
		return nil
	}
	ip := net.ParseIP(source)
	if ip == nil {
		return nil
	}
	if strings.HasPrefix(network, "udp") {
		return &net.UDPAddr{IP: ip}
	}
	return &net.TCPAddr{IP: ip}
}

// newBoundDialer returns a net.Dialer for network ("tcp" or "udp") that
// honours bc: LocalAddr pins the source address (-S) and Control pins the
// physical egress interface (-I) via the same setsockopt the ICMP path uses.
//
// Both are opt-in: with a zero BindConfig the returned dialer is identical to
// the plain &net.Dialer{Timeout: timeout} that preceded this feature, and on
// platforms without an interface-binding facility the Control hook resolves to
// a no-op (see bindif_other.go), so behavior there is unchanged too.
func newBoundDialer(network string, timeout time.Duration, bc BindConfig) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if addr := localAddrFor(network, bc.Source); addr != nil {
		d.LocalAddr = addr
	}
	if bc.Interface != "" {
		ifaceName := bc.Interface
		// Control receives the family-qualified network ("tcp4", "udp6", ...)
		// the dial actually resolved to, which is the only place the address
		// family is known — bc alone cannot tell us, since a hostname target
		// may resolve either way.
		d.Control = func(network, _ string, c syscall.RawConn) error {
			bindRawConnToInterfaceFn(c, ifaceName, strings.HasSuffix(network, "6"))
			return nil
		}
	}
	return d
}

// NewBoundDialer is newBoundDialer for callers outside this package that
// build their own dials — notably cmd/main's DNS resolver, which needs the
// same -S/-I treatment as the ICMP pinger and the port/HTTP checkers so name
// resolution leaves via the same egress path.
func NewBoundDialer(network string, timeout time.Duration, bc BindConfig) *net.Dialer {
	return newBoundDialer(network, timeout, bc)
}

// newBoundTransport returns an http.Transport whose dials honour bc, or nil
// when bc requests no binding — in which case callers must leave
// http.Client.Transport unset so http.DefaultTransport stays in play, exactly
// as before -S/-I were wired into the HTTP checker. Cloning the default
// transport (rather than building one from scratch) preserves proxy support,
// HTTP/2 negotiation and the default connection-pool limits.
func newBoundTransport(timeout time.Duration, bc BindConfig) *http.Transport {
	if bc.IsZero() {
		return nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = newBoundDialer("tcp", timeout, bc).DialContext
	return tr
}
