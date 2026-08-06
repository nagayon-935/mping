package pinger

import (
	"net"
	"syscall"
)

// bindRawConnToInterfaceFn is a seam over the platform-specific
// bindRawConnToInterface (see bindif_darwin.go / bindif_linux.go /
// bindif_other.go). Tests replace it to assert that the net.Dialer.Control
// hooks built by newBoundDialer dispatch with the right interface name and
// address family, without needing privileges, real interfaces, or a real
// connection. It mirrors bindToInterfaceFn, the equivalent seam for the ICMP
// net.PacketConn path.
var bindRawConnToInterfaceFn = bindRawConnToInterface

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
