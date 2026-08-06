//go:build !darwin && !linux

package pinger

import (
	"net"
	"syscall"
)

// bindToInterface is a no-op on platforms without a native "bind socket to
// physical interface" facility exposed through golang.org/x/sys/unix (e.g.
// Windows, the BSDs other than Darwin). mping falls back to the pre-existing
// source-IP bind performed by Pinger.Start via p.Source on these platforms,
// leaving prior behavior on unsupported platforms completely unchanged.
func bindToInterface(_ net.PacketConn, _ string, _ bool) {}

// bindRawConnToInterface is the no-op counterpart for the net.Dialer.Control
// path used by the port and HTTP checkers (see dialbind.go). On these
// platforms -I therefore has no effect on TCP/UDP checks either: they keep
// whatever egress the routing table selects, narrowed only by the -S source
// bind, which is portable. That is exactly the pre-existing behavior.
func bindRawConnToInterface(_ syscall.RawConn, _ string, _ bool) {}
