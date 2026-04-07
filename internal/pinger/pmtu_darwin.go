//go:build darwin

package pinger

import (
	"net"

	"golang.org/x/sys/unix"
)

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
