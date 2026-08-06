//go:build linux

package pinger

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// bindToInterface binds the underlying socket of c to the network interface
// named ifaceName using SO_BINDTODEVICE, so outgoing packets always leave via
// that physical interface regardless of what the routing table would
// otherwise select and regardless of the same address being assigned to more
// than one interface. This is Linux's counterpart to Apple's IP_BOUND_IF /
// IPV6_BOUND_IF (see bindif_darwin.go): unlike binding the socket to a source
// address, it constrains the egress interface itself. SO_BINDTODEVICE works
// identically for IPv4 and IPv6 sockets, so isIPv6 is unused on this
// platform.
//
// Failures are non-fatal: if c doesn't expose a raw socket, or the
// setsockopt call itself fails (e.g. missing CAP_NET_RAW), mping silently
// falls back to the source-IP bind Pinger.Start already performs via
// p.Source — the same non-fatal treatment as the pre-existing
// ipv4.FlagTTL / ipv6.FlagHopLimit control-message setup in Start().
func bindToInterface(c net.PacketConn, ifaceName string, _ bool) {
	if ifaceName == "" {
		return
	}
	sc, ok := c.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		return
	}
	_ = rawConn.Control(func(fd uintptr) {
		_ = unix.BindToDevice(int(fd), ifaceName)
	})
}
