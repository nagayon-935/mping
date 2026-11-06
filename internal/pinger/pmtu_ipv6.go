package pinger

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	ipv4EchoOverhead     = ipv4.HeaderLen + icmpEchoHeaderLen
	icmpEchoHeaderLen    = 8
	ipv6EchoOverhead     = ipv6.HeaderLen + icmpEchoHeaderLen
	ipv6MinimumMTU       = 1280
	pmtuMaxAttempts      = 8
	pmtuDiscoveryTimeout = 3 * time.Second
	pmtuSourceProbe      = "probe"
	pmtuSourceOS         = "os"
	pmtuStatusProbed     = "probed"
	pmtuStatusEstimated  = "estimated"
	pmtuStatusConfirmed  = "confirmed"
)

// PathMTUResult separates an IP packet's MTU from its ICMP payload budget.
// An OS estimate is confirmed only by an unfragmented echo of that size.
type PathMTUResult = stats.PathMTUInfo

// DiscoverPathMTU uses kernel PMTUD for IPv6 and the existing DF search for IPv4.
func (p *Pinger) DiscoverPathMTU(ctx context.Context, dest string, start, minPayload int, logf func(string)) (PathMTUResult, error) {
	dst, err := p.resolveIPAddrContext(ctx, "ip", dest)
	if err != nil {
		return PathMTUResult{}, err
	}
	if dst.IP.To4() != nil {
		payload, hop, err := p.discoverIPv4MaxPayload(ctx, dst, start, minPayload, logf)
		if err != nil {
			return PathMTUResult{}, err
		}
		return PathMTUResult{IP: dst.String(), MTU: payload + ipv4EchoOverhead, Payload: payload, Source: pmtuSourceProbe, Status: pmtuStatusProbed, BottleneckIP: hop}, nil
	}
	ctx, cancel := p.lookupContext(ctx, pmtuDiscoveryTimeout)
	defer cancel()
	c, err := openIPv6PMTUSocket(ctx, p, dst)
	if err != nil {
		return PathMTUResult{}, err
	}
	defer c.Close()
	defer closeOnCancel(ctx, func() { _ = c.Close() })()
	return p.discoverIPv6PathMTU(ctx, c, dst, logf)
}

type ipv6PMTUSocket interface {
	Write([]byte) (int, error)
	Read([]byte) (int, error)
	SetDeadline(time.Time) error
	Close() error
	PathMTU() (int, error)
}

type nativeIPv6PMTUSocket struct{ *net.IPConn }

func (c nativeIPv6PMTUSocket) PathMTU() (int, error) { return ipv6SocketPathMTU(c.IPConn) }

// dialIPv6PMTUConn is a seam so tests can verify that the raw socket is
// created inside a privileged section without real privileges.
var dialIPv6PMTUConn = func(ctx context.Context, d *net.Dialer, address string) (net.Conn, error) {
	return d.DialContext(ctx, "ip6:ipv6-icmp", address)
}

// A connected socket makes the kernel's PMTU query destination-specific.
// Never silently continue when the OS cannot prohibit sender fragmentation.
var openIPv6PMTUSocket = func(ctx context.Context, p *Pinger, dst *net.IPAddr) (ipv6PMTUSocket, error) {
	dialer, err := p.ipv6PMTUDialer()
	if err != nil {
		return nil, err
	}
	// Like privilegedListenPacket: this runs after ping has started, so it
	// must not overlap an AsRealUser section that has dropped privileges.
	var conn net.Conn
	err = runPrivileged(func() error {
		var dialErr error
		conn, dialErr = dialIPv6PMTUConn(ctx, dialer, dst.String())
		return dialErr
	})
	if err != nil {
		return nil, fmt.Errorf("open IPv6 PMTU socket: %w", err)
	}
	c, ok := conn.(*net.IPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("open IPv6 PMTU socket: unexpected connection type %T", conn)
	}
	if err := configureICMPBuffers(c); err != nil {
		c.Close()
		return nil, err
	}
	applyICMPv6Filter(ipv6.NewPacketConn(c))
	return nativeIPv6PMTUSocket{c}, nil
}

func (p *Pinger) discoverIPv6PathMTU(ctx context.Context, c ipv6PMTUSocket, dst *net.IPAddr, logf func(string)) (PathMTUResult, error) {
	result := PathMTUResult{IP: dst.String(), Source: pmtuSourceOS, Status: pmtuStatusEstimated}
	id, release := p.acquireTraceID()
	if id < 0 {
		return result, fmt.Errorf("PMTU probe IDs exhausted")
	}
	defer release()
	buf := make([]byte, receiverBufferSize)
	// The IPv6 payload length excludes the 40-byte base header. In
	// particular, Linux loopback's 65536-byte MTU is not a jumbogram.
	const maxProbeMTU = receiverBufferSize + ipv6.HeaderLen
	for seq := 1; seq <= pmtuMaxAttempts; seq++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		mtu, err := c.PathMTU()
		if err != nil {
			return result, fmt.Errorf("query IPv6 path MTU: %w", err)
		}
		if mtu < ipv6MinimumMTU {
			return result, fmt.Errorf("invalid IPv6 path MTU %d", mtu)
		}
		result.MTU, result.Payload = mtu, mtu-ipv6EchoOverhead
		if logf != nil {
			logf(fmt.Sprintf("[PMTU] %s: MTU=%d (OS estimate)", dst, mtu))
		}
		// Bound memory and wire size even for unusual jumbo loopback routes.
		payload := make([]byte, min(mtu, maxProbeMTU)-ipv6EchoOverhead)
		copy(payload, fmt.Sprintf("MPING-PMTU-%d-%d-%d", id, seq, time.Now().UnixNano()))
		message := icmp.Message{Type: ipv6.ICMPTypeEchoRequest, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}
		packet, err := marshalProbe(&message)
		if err != nil {
			return result, err
		}
		deadline := time.Now().Add(pmtuProbeTimeout)
		if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
			deadline = limit
		}
		if err := c.SetDeadline(deadline); err != nil {
			return result, err
		}
		_, err = c.Write(packet)
		if err != nil && !isMTUTooLarge(err) {
			return result, err
		}
		if err == nil {
			matched, err := waitIPv6PMTUReply(ctx, c, buf, id, seq, payload)
			if err != nil {
				return result, err
			}
			if matched {
				if mtu <= maxProbeMTU {
					result.Status = pmtuStatusConfirmed
				}
				if logf != nil {
					logf(fmt.Sprintf("[PMTU] %s: MTU=%d, payload=%d (%s)", dst, mtu, result.Payload, result.Status))
				}
				return result, nil
			}
		}
		next, err := c.PathMTU()
		if err != nil {
			return result, fmt.Errorf("query IPv6 path MTU after probe: %w", err)
		}
		if next >= mtu {
			// A timeout supplies no evidence that the size is too large.
			return result, nil
		}
		if next < ipv6MinimumMTU {
			return result, fmt.Errorf("invalid IPv6 path MTU %d", next)
		}
		result.MTU, result.Payload = next, next-ipv6EchoOverhead
	}
	return result, nil
}

// waitIPv6PMTUReply waits for an echo matching the complete probe payload.
// A timeout (including a wrapped one), EMSGSIZE, or matching Packet Too Big
// leaves the OS estimate to be queried again by the caller.
func waitIPv6PMTUReply(ctx context.Context, c ipv6PMTUSocket, buf []byte, id, seq int, payload []byte) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, readErr := c.Read(buf)
		if readErr != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if !isTimeout(readErr) {
				if !isMTUTooLarge(readErr) {
					return false, readErr
				}
			}
			return false, nil
		}
		msg, parseErr := icmp.ParseMessage(protoICMPv6, buf[:n])
		if parseErr != nil {
			continue
		}
		if echo, ok := msg.Body.(*icmp.Echo); msg.Type == ipv6.ICMPTypeEchoReply && ok && echo.ID == id && echo.Seq == seq && bytes.Equal(echo.Data, payload) {
			return true, nil
		}
		if msg.Type == ipv6.ICMPTypePacketTooBig {
			gotID, gotSeq, ok := extractEchoIDSeq(msg)
			if ok && gotID == id && gotSeq == seq {
				return false, nil // Kernel processes the PTB; caller queries its updated estimate.
			}
		}
	}
}

// ipv6PMTUDialer preserves OS source selection where native interface binding
// is available, and supplies a family-specific LocalAddr on other platforms.
func (p *Pinger) ipv6PMTUDialer() (*net.Dialer, error) {
	dialer := &net.Dialer{Timeout: time.Second}
	source := p.Source
	if source == "" && p.Interface != "" && !interfaceBindingSupported {
		var err error
		source, err = p.interfaceSource(true)
		if err != nil {
			return nil, err
		}
	}
	if source != "" {
		addr, err := netip.ParseAddr(source)
		if err != nil || !addr.Unmap().Is6() {
			return nil, fmt.Errorf("IPv6 PMTU requires an IPv6 source")
		}
		dialer.LocalAddr = &net.IPAddr{IP: net.IP(addr.AsSlice()), Zone: addr.Zone()}
	}
	dialer.Control = func(_, _ string, raw syscall.RawConn) error {
		// Control runs inside the privileged dial: never re-enter it here.
		bindRawConnToInterfaceHeldFn(raw, p.Interface, true)
		var optionErr error
		if err := raw.Control(func(fd uintptr) { optionErr = setIPv6PMTUDiscoveryFn(int(fd)) }); err != nil {
			return err
		}
		return optionErr
	}
	return dialer, nil
}

// setIPv6PMTUDiscoveryFn is a seam over the platform setIPv6PMTUDiscovery
// (pmtu_darwin.go / pmtu_linux.go / pmtu_other.go) so tests can run the dial's
// Control hook without a real socket.
var setIPv6PMTUDiscoveryFn = setIPv6PMTUDiscovery
