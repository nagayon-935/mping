//go:build darwin

package pinger

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func setIPv6PMTUDiscovery(fd int) error {
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_USE_MIN_MTU, 0); err != nil {
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
	err = raw.Control(func(fd uintptr) {
		info, err := unix.GetsockoptIPv6MTUInfo(int(fd), unix.IPPROTO_IPV6, unix.IPV6_PATHMTU)
		queryErr = err
		if err == nil {
			mtu = int(info.Mtu)
		}
	})
	if err != nil {
		return 0, err
	}
	return mtu, queryErr
}

// setSocketDontFragment sets IP_DONTFRAG (0x43 / 67) on the underlying socket
// so that the macOS kernel returns EMSGSIZE instead of silently fragmenting
// packets that exceed the outgoing interface MTU.
//
// On macOS, setting the DF bit in the IP header via IP_HDRINCL is not
// sufficient — the kernel ignores it and fragments transparently. The
// IP_DONTFRAG socket option is the only reliable way to enforce DF on Darwin.
func setSocketDontFragment(c net.PacketConn) {
	ipConn, ok := c.(*net.IPConn)
	if !ok {
		return
	}
	rawConn, err := ipConn.SyscallConn()
	if err != nil {
		return
	}
	_ = rawConn.Control(func(fd uintptr) {
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_DONTFRAG, 1)
	})
}
