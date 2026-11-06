//go:build linux

package pinger

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func setIPv6PMTUDiscovery(fd int) error {
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MTU_DISCOVER, unix.IPV6_PMTUDISC_DO); err != nil {
		return err
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_DONTFRAG, 1)
}

func ipv6SocketPathMTU(c syscall.Conn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var mtu int
	var queryErr error
	err = raw.Control(func(fd uintptr) { mtu, queryErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_MTU) })
	if err != nil {
		return 0, err
	}
	return mtu, queryErr
}

// setSocketDontFragment is a no-op on Linux because the kernel honours the DF
// bit written in the IP header when IP_HDRINCL is active, returning EMSGSIZE
// when the packet exceeds the outgoing interface MTU.
func setSocketDontFragment(_ net.PacketConn) {}
