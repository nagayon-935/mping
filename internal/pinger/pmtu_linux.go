//go:build linux

package pinger

import "net"

// setSocketDontFragment is a no-op on Linux because the kernel honours the DF
// bit written in the IP header when IP_HDRINCL is active, returning EMSGSIZE
// when the packet exceeds the outgoing interface MTU.
func setSocketDontFragment(_ net.PacketConn) {}
