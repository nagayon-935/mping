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

// bindRawConnToInterface binds a socket, already reduced to its
// syscall.RawConn, to the network interface named ifaceName using IP_BOUND_IF
// (IPv4) / IPV6_BOUND_IF (IPv6) - macOS's setsockopt equivalent of ping(8)'s
// -b and ping6(8)'s -B flags. Unlike a source-address bind (net.ListenPacket's
// bindAddr), this pins the physical egress interface itself, ignoring the
// routing table and unaffected by the same address being assigned to more than
// one interface.
//
// The RawConn form is what net.Dialer.Control hands us, used by the port and
// HTTP checkers (see dialbind.go) so their TCP/UDP connections leave via the
// same physical interface as the ICMP sockets (see bindToInterface in
// bindif.go). Failures stay non-fatal for the reasons documented there.
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
