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
	ip := net.ParseIP(source) // nil for "" as well as a non-literal
	if ip == nil {
		return nil
	}
	if strings.HasPrefix(network, "udp") {
		return &net.UDPAddr{IP: ip}
	}
	return &net.TCPAddr{IP: ip}
}

// NewBoundDialer returns a net.Dialer for network ("tcp" or "udp") that
// honours bc: LocalAddr pins the source address (-S) and Control pins the
// physical egress interface (-I) via the same setsockopt the ICMP path uses.
// It is exported for callers that build their own dials - notably cmd/main's
// DNS resolver, which needs the same -S/-I treatment as the ICMP pinger and
// the port/HTTP checkers so name resolution leaves via the same egress path.
//
// Both are opt-in: with a zero BindConfig the returned dialer is identical to
// the plain &net.Dialer{Timeout: timeout} that preceded this feature, and on
// platforms without an interface-binding facility the Control hook resolves to
// a no-op (see bindif_other.go), so behavior there is unchanged too.
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
			bindRawConnToInterfaceFn(c, bc.Interface, strings.HasSuffix(network, "6"))
			return nil
		}
	}
	return d
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
	tr.DialContext = NewBoundDialer("tcp", timeout, bc).DialContext
	return tr
}
