package pinger

import (
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/nagayon-935/mping/internal/privilege"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// packetConnV4 is the subset of *ipv4.PacketConn used for IPv4 probes.
type packetConnV4 interface {
	ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error)
	WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error)
	SetReadDeadline(t time.Time) error
	Close() error
	SetControlMessage(cf ipv4.ControlFlags, on bool) error
}

// packetConnV6 is the subset of *ipv6.PacketConn used for IPv6 probes.
type packetConnV6 interface {
	ReadFrom(b []byte) (int, *ipv6.ControlMessage, net.Addr, error)
	WriteTo(b []byte, cm *ipv6.ControlMessage, dst net.Addr) (int, error)
	SetReadDeadline(t time.Time) error
	Close() error
	SetControlMessage(cf ipv6.ControlFlags, on bool) error
	SetICMPFilter(f *ipv6.ICMPFilter) error
}

type listenPacketFunc func(network, address string) (net.PacketConn, error)

// runPrivileged is the single entry point this package uses for
// privilege.Privileged. It is a seam so tests can observe every privileged
// section, in particular reentry: privilege's RWMutex is not reentrant, and a
// nested section deadlocks once an AsRealUser section waits in between.
var runPrivileged = privilege.Privileged

// inPrivileged runs a best-effort setsockopt-style fn inside runPrivileged.
// Binding to an interface needs CAP_NET_RAW on Linux, which a setuid-root
// run only has while not inside privilege.AsRealUser.
func inPrivileged(fn func()) {
	_ = runPrivileged(func() error {
		fn()
		return nil
	})
}

// privilegedListenPacket is the default listenPacketFunc: raw ICMP sockets
// need root (or CAP_NET_RAW), so they are opened inside privilege.Privileged,
// never while a setuid-root run has dropped to the invoking user for file I/O.
func privilegedListenPacket(network, address string) (net.PacketConn, error) {
	var c net.PacketConn
	err := runPrivileged(func() error {
		var err error
		c, err = net.ListenPacket(network, address)
		return err
	})
	return c, err
}

// bindToInterfaceFn is a seam over the platform-specific bindToInterface
// implementation (bindif_linux.go / bindif_darwin.go / bindif_other.go),
// letting tests verify that Start(), openHopSocketAddr(), and canSendPayload()
// dispatch to it with the correct interface name and address family without
// requiring root privileges, real sockets, or real network interfaces.
var bindToInterfaceFn = func(c net.PacketConn, ifaceName string, isIPv6 bool) {
	inPrivileged(func() { bindToInterface(c, ifaceName, isIPv6) })
}

// listenICMP opens a raw ICMP (v4) or ICMPv6 (v6) socket bound to the
// configured Source address, or to the wildcard address when none is set.
// Interface binding is left to the caller via bindToInterfaceFn.
func (p *Pinger) listenICMP(v6 bool) (net.PacketConn, error) {
	source, err := p.socketSource(v6)
	if err != nil {
		return nil, err
	}
	return p.listenICMPSource(v6, source)
}

func (p *Pinger) listenICMPSource(v6 bool, source string) (net.PacketConn, error) {
	network, bindAddr := "ip4:icmp", "0.0.0.0"
	if v6 {
		network, bindAddr = "ip6:ipv6-icmp", "::"
	}
	if source != "" {
		bindAddr = source
	}
	c, err := p.listenPacket(network, bindAddr)
	if err != nil {
		return nil, err
	}
	if err := configureICMPBuffers(c); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// openProbeSockets allows a dual-stack pinger to start when either family opens.
func (p *Pinger) openProbeSockets() error {
	var errV4, errV6 error

	// Initialize IPv4
	if p.Source == "" || isIPv4(p.Source) {
		c, err := p.listenICMP(false)
		if err == nil {
			// Non-fatal: true interface binding may be unsupported on this
			// platform, or fail (e.g. missing privileges); the source-IP
			// bind above still applies regardless.
			bindToInterfaceFn(c, p.Interface, false)
			p.connV4 = ipv4.NewPacketConn(c)
			// Non-fatal: TTL control message may not be available on all platforms.
			_ = p.connV4.SetControlMessage(ipv4.FlagTTL, true)
		} else {
			errV4 = err
		}
	}

	// Initialize IPv6
	if p.Source == "" || !isIPv4(p.Source) {
		c, err := p.listenICMP(true)
		if err == nil {
			// Non-fatal, same rationale as the IPv4 block above.
			bindToInterfaceFn(c, p.Interface, true)
			p.connV6 = ipv6.NewPacketConn(c)
			// Non-fatal: the hop limit control message may not be available
			// on all platforms.
			_ = p.connV6.SetControlMessage(ipv6.FlagHopLimit, true)
			applyICMPv6Filter(p.connV6)
		} else {
			errV6 = err
		}
	}

	if p.connV4 == nil && p.connV6 == nil {
		return fmt.Errorf("failed to initialize pinger: v4=%v, v6=%v", errV4, errV6)
	}
	return nil
}

// A userspace receive slice is insufficient if the kernel drops the packet
// first. Darwin raw sockets default to 8192-byte send/receive queues, which
// can reject a loopback PMTU probe or drop its echo (including control data).
// Leave room for a maximum-size packet and kernel ancillary-data overhead.
func configureICMPBuffers(conn any) error {
	c, ok := conn.(interface {
		SetReadBuffer(int) error
		SetWriteBuffer(int) error
	})
	if !ok {
		return nil // Socket test doubles need not expose kernel buffer options.
	}
	const size = 2 * receiverBufferSize
	if err := c.SetReadBuffer(size); err != nil {
		return fmt.Errorf("set ICMP receive buffer: %w", err)
	}
	if err := c.SetWriteBuffer(size); err != nil {
		return fmt.Errorf("set ICMP send buffer: %w", err)
	}
	return nil
}

// bindRawConnToInterfaceFn is a seam over the platform-specific
// bindRawConnToInterface (see bindif_darwin.go / bindif_linux.go /
// bindif_other.go). Tests replace it to assert that the net.Dialer.Control
// hooks built by NewBoundDialer dispatch with the right interface name and
// address family, without needing privileges, real interfaces, or a real
// connection. It mirrors bindToInterfaceFn, the equivalent seam for the ICMP
// net.PacketConn path.
var bindRawConnToInterfaceFn = func(c syscall.RawConn, ifaceName string, isIPv6 bool) {
	inPrivileged(func() { bindRawConnToInterface(c, ifaceName, isIPv6) })
}

// bindRawConnToInterfaceHeldFn binds without entering privilege.Privileged.
// Use it from code already running inside a privileged section, such as a
// net.Dialer.Control hook of a dial wrapped in privilege.Privileged: the
// privilege RWMutex is not reentrant, and a nested read lock deadlocks once
// an AsRealUser section is waiting for the write lock in between.
var bindRawConnToInterfaceHeldFn = bindRawConnToInterface

// rawConnOf extracts the syscall.RawConn behind a net.PacketConn, reporting
// ok=false for conns that don't expose one (notably the fakes used in this
// package's seam-based unit tests). Callers treat !ok as "skip the bind",
// matching bindToInterface's non-fatal contract.
func rawConnOf(c net.PacketConn) (syscall.RawConn, bool) {
	sc, ok := c.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return nil, false
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		return nil, false
	}
	return rawConn, true
}

// bindToInterface binds the underlying socket of c to the network interface
// named ifaceName, so outgoing packets leave via that physical interface
// regardless of what the routing table would otherwise select and regardless
// of the same address being assigned to more than one interface. The
// platform-specific part lives in bindRawConnToInterface (bindif_darwin.go:
// IP_BOUND_IF / IPV6_BOUND_IF; bindif_linux.go: SO_BINDTODEVICE;
// bindif_other.go: a no-op).
//
// Failures are non-fatal: if c doesn't expose a raw socket (including a nil
// c), the interface can't be resolved, or the setsockopt call itself fails
// (e.g. missing CAP_NET_RAW), mping silently falls back to the source-IP bind
// Pinger.Start already performs via p.Source - the same non-fatal treatment
// as the ipv4.FlagTTL / ipv6.FlagHopLimit control-message setup in Start().
func bindToInterface(c net.PacketConn, ifaceName string, isIPv6 bool) {
	rawConn, ok := rawConnOf(c)
	if !ok {
		return
	}
	bindRawConnToInterface(rawConn, ifaceName, isIPv6)
}
