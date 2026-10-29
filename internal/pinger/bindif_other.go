//go:build !darwin && !linux

package pinger

import "syscall"

// bindRawConnToInterface is a no-op on platforms without a native "bind socket
// to physical interface" facility exposed through golang.org/x/sys/unix (e.g.
// Windows, the BSDs other than Darwin). mping falls back to the pre-existing
// source-IP bind performed by Pinger.Start via p.Source, leaving prior
// behavior on unsupported platforms completely unchanged. This covers both the
// ICMP path (via bindToInterface in bindif.go) and the net.Dialer.Control path
// used by the port and HTTP checkers (see dialbind.go): -I therefore has no
// effect on TCP/UDP checks either, which keep whatever egress the routing
// table selects, narrowed only by the -S source bind, which is portable.
func bindRawConnToInterface(_ syscall.RawConn, _ string, _ bool) {}
