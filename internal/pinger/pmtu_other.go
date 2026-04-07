//go:build !darwin && !linux

package pinger

import "net"

// setSocketDontFragment is a no-op on unsupported platforms.
func setSocketDontFragment(_ net.PacketConn) {}
