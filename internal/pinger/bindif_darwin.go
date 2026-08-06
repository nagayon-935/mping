//go:build darwin

package pinger

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// interfaceByNameFn resolves an interface name to its OS-assigned index;
// overridden in tests so the interface-lookup path can be exercised without
// depending on the host's real network interfaces.
var interfaceByNameFn = net.InterfaceByName

// bindToInterface binds the underlying socket of c to the network interface
// named ifaceName using IP_BOUND_IF (IPv4) / IPV6_BOUND_IF (IPv6) — macOS's
// setsockopt equivalent of ping(8)'s -b and ping6(8)'s -B flags. Unlike a
// source-address bind (net.ListenPacket's bindAddr), this pins the physical
// egress interface itself, ignoring the routing table and unaffected by the
// same address being assigned to more than one interface.
//
// Failures are non-fatal, mirroring bindif_linux.go's rationale: if the
// interface name doesn't resolve, c doesn't expose a raw socket, or the
// setsockopt call fails, mping silently falls back to the source-IP bind
// Pinger.Start already performs via p.Source.
func bindToInterface(c net.PacketConn, ifaceName string, isIPv6 bool) {
	rawConn, ok := rawConnOf(c)
	if !ok {
		return
	}
	bindRawConnToInterface(rawConn, ifaceName, isIPv6)
}

// bindRawConnToInterface is bindToInterface for a socket already reduced to
// its syscall.RawConn — the form net.Dialer.Control hands us, used by the port
// and HTTP checkers (see dialbind.go) so their TCP/UDP connections leave via
// the same physical interface as the ICMP sockets above. Failures stay
// non-fatal for the reasons documented on bindToInterface.
func bindRawConnToInterface(rawConn syscall.RawConn, ifaceName string, isIPv6 bool) {
	if ifaceName == "" || rawConn == nil {
		return
	}
	iface, err := interfaceByNameFn(ifaceName)
	if err != nil {
		return
	}
	_ = rawConn.Control(func(fd uintptr) {
		if isIPv6 {
			_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, iface.Index)
			return
		}
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
	})
}
