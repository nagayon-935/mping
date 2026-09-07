package pinger

import (
	"net"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// Datagram ICMP sockets are available to ordinary users on Darwin and Linux.
// Their destination address must be a UDPAddr even though the payload is ICMP.
type unprivilegedV4Conn struct{ *ipv4.PacketConn }

func (c *unprivilegedV4Conn) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) {
	return c.PacketConn.WriteTo(b, cm, unprivilegedDestination(dst))
}

type unprivilegedV6Conn struct{ *ipv6.PacketConn }

func (c *unprivilegedV6Conn) WriteTo(b []byte, cm *ipv6.ControlMessage, dst net.Addr) (int, error) {
	return c.PacketConn.WriteTo(b, cm, unprivilegedDestination(dst))
}

func unprivilegedDestination(dst net.Addr) net.Addr {
	if ip, ok := dst.(*net.IPAddr); ok {
		return &net.UDPAddr{IP: ip.IP, Zone: ip.Zone}
	}
	return dst
}

func listenUnprivilegedV4(address string) (PacketConnV4, error) {
	c, err := icmp.ListenPacket("udp4", address)
	if err != nil {
		return nil, err
	}
	return &unprivilegedV4Conn{PacketConn: c.IPv4PacketConn()}, nil
}

func listenUnprivilegedV6(address string) (PacketConnV6, error) {
	c, err := icmp.ListenPacket("udp6", address)
	if err != nil {
		return nil, err
	}
	return &unprivilegedV6Conn{PacketConn: c.IPv6PacketConn()}, nil
}
