//go:build !darwin && !linux

package pinger

import "net"

// bindToInterface is a no-op on platforms without a native "bind socket to
// physical interface" facility exposed through golang.org/x/sys/unix (e.g.
// Windows, the BSDs other than Darwin). mping falls back to the pre-existing
// source-IP bind performed by Pinger.Start via p.Source on these platforms,
// leaving prior behavior on unsupported platforms completely unchanged.
func bindToInterface(_ net.PacketConn, _ string, _ bool) {}
