package pinger

import (
	"net"
	"syscall"

	"github.com/nagayon-935/mping/internal/privilege"
)

// bindRawConnToInterfaceFn is a seam over the platform-specific
// bindRawConnToInterface (see bindif_darwin.go / bindif_linux.go /
// bindif_other.go). Tests replace it to assert that the net.Dialer.Control
// hooks built by NewBoundDialer dispatch with the right interface name and
// address family, without needing privileges, real interfaces, or a real
// connection. It mirrors bindToInterfaceFn, the equivalent seam for the ICMP
// net.PacketConn path.
var bindRawConnToInterfaceFn = func(c syscall.RawConn, ifaceName string, isIPv6 bool) {
	_ = privilege.Privileged(func() error {
		bindRawConnToInterface(c, ifaceName, isIPv6)
		return nil
	})
}

// rawConnOf extracts the syscall.RawConn behind a net.PacketConn, reporting
// ok=false for conns that don't expose one (notably the fakes used in this
// package's seam-based unit tests). Callers treat !ok as "skip the bind",
// matching bindToInterface's non-fatal contract.
func rawConnOf(c net.PacketConn) (syscall.RawConn, bool) {
	sc, ok := c.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return nil, false
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		return nil, false
	}
	return rawConn, true
}

// bindToInterface binds the underlying socket of c to the network interface
// named ifaceName, so outgoing packets leave via that physical interface
// regardless of what the routing table would otherwise select and regardless
// of the same address being assigned to more than one interface. The
// platform-specific part lives in bindRawConnToInterface (bindif_darwin.go:
// IP_BOUND_IF / IPV6_BOUND_IF; bindif_linux.go: SO_BINDTODEVICE;
// bindif_other.go: a no-op).
//
// Failures are non-fatal: if c doesn't expose a raw socket (including a nil
// c), the interface can't be resolved, or the setsockopt call itself fails
// (e.g. missing CAP_NET_RAW), mping silently falls back to the source-IP bind
// Pinger.Start already performs via p.Source - the same non-fatal treatment
// as the pre-existing ipv4.FlagTTL / ipv6.FlagHopLimit control-message setup
// in Start().
func bindToInterface(c net.PacketConn, ifaceName string, isIPv6 bool) {
	rawConn, ok := rawConnOf(c)
	if !ok {
		return
	}
	bindRawConnToInterface(rawConn, ifaceName, isIPv6)
}
