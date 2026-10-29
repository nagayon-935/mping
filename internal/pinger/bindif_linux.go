//go:build linux

package pinger

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// bindRawConnToInterface binds a socket, already reduced to its
// syscall.RawConn, to the network interface named ifaceName using
// SO_BINDTODEVICE, so outgoing packets always leave via that physical
// interface regardless of what the routing table would otherwise select. This
// is Linux's counterpart to Apple's IP_BOUND_IF / IPV6_BOUND_IF (see
// bindif_darwin.go): unlike binding the socket to a source address, it
// constrains the egress interface itself.
//
// The RawConn form is what net.Dialer.Control hands us, used by the port and
// HTTP checkers (see dialbind.go) so their TCP/UDP connections leave via the
// same physical interface as the ICMP sockets (see bindToInterface in
// bindif.go). SO_BINDTODEVICE is family-agnostic so isIPv6 is unused here, and
// failures are deliberately non-fatal.
func bindRawConnToInterface(rawConn syscall.RawConn, ifaceName string, _ bool) {
	if ifaceName == "" || rawConn == nil {
		return
	}
	_ = rawConn.Control(func(fd uintptr) {
		_ = unix.BindToDevice(int(fd), ifaceName)
	})
}
