//go:build !darwin && !linux

package pinger

import (
	"fmt"
	"net"
	"syscall"
)

func setIPv6PMTUDiscovery(fd int) error {
	return fmt.Errorf("IPv6 PMTU discovery is unsupported on this OS")
}
func ipv6SocketPathMTU(c syscall.Conn) (int, error) {
	return 0, fmt.Errorf("IPv6 PMTU query is unsupported on this OS")
}

// setSocketDontFragment is a no-op on unsupported platforms.
func setSocketDontFragment(_ net.PacketConn) {}
