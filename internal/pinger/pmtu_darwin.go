//go:build darwin

package pinger

import (
	"net"
	"syscall"
)

// ipDontFrag is the IP_DONTFRAG socket option value on macOS/Darwin.
// When set, the kernel will return EMSGSIZE instead of silently fragmenting
// packets that exceed the outgoing interface MTU, even for raw sockets with
// IP_HDRINCL where the DF bit in the IP header is not honoured by the kernel.
const ipDontFrag = 0x1e // 30

// setSocketDontFragment sets IP_DONTFRAG on the underlying socket so that the
// macOS kernel enforces the do-not-fragment constraint at the socket level.
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
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, ipDontFrag, 1)
	})
}
