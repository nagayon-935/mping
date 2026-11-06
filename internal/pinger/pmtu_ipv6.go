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
	"golang.org/x/net/ipv6"
)

// PathMTUResult separates an IP packet's MTU from its ICMP payload budget.
// An OS estimate is confirmed only by an unfragmented echo of that size.
type PathMTUResult = stats.PathMTUInfo

// DiscoverPathMTU uses kernel PMTUD for IPv6 and the existing DF search for IPv4.
func (p *Pinger) DiscoverPathMTU(ctx context.Context, dest string, start, min int, logf func(string)) (PathMTUResult, error) {
	dst, err := p.resolveIPAddrContext(ctx, "ip", dest)
	if err != nil {
		return PathMTUResult{}, err
	}
	if dst.IP.To4() != nil {
		payload, hop, err := p.discoverIPv4MaxPayload(ctx, dst, start, min, logf)
		if err != nil {
			return PathMTUResult{}, err
		}
		return PathMTUResult{IP: dst.String(), MTU: payload + 28, Payload: payload, Source: "probe", Status: "probed", BottleneckIP: hop}, nil
	}
	ctx, cancel := p.lookupContext(ctx, 3*time.Second)
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

// A connected socket makes the kernel's PMTU query destination-specific.
// Never silently continue when the OS cannot prohibit sender fragmentation.
var openIPv6PMTUSocket = func(ctx context.Context, p *Pinger, dst *net.IPAddr) (ipv6PMTUSocket, error) {
	dialer := net.Dialer{Timeout: time.Second}
	if p.Source != "" {
		addr, err := netip.ParseAddr(p.Source)
		if err != nil || !addr.Unmap().Is6() {
			return nil, fmt.Errorf("IPv6 PMTU requires an IPv6 source")
		}
		dialer.LocalAddr = &net.IPAddr{IP: net.IP(addr.AsSlice()), Zone: addr.Zone()}
	}
	dialer.Control = func(_, _ string, raw syscall.RawConn) error {
		bindRawConnToInterfaceFn(raw, p.Interface, true)
		var optionErr error
		if err := raw.Control(func(fd uintptr) { optionErr = setIPv6PMTUDiscovery(int(fd)) }); err != nil {
			return err
		}
		return optionErr
	}
	conn, err := dialer.DialContext(ctx, "ip6:ipv6-icmp", dst.String())
	if err != nil {
		return nil, fmt.Errorf("open IPv6 PMTU socket: %w", err)
	}
	c := conn.(*net.IPConn)
	if err := configureICMPBuffers(c); err != nil {
		c.Close()
		return nil, err
	}
	applyICMPv6Filter(ipv6.NewPacketConn(c))
	return nativeIPv6PMTUSocket{c}, nil
}

func (p *Pinger) discoverIPv6PathMTU(ctx context.Context, c ipv6PMTUSocket, dst *net.IPAddr, logf func(string)) (PathMTUResult, error) {
	result := PathMTUResult{IP: dst.String(), Source: "os", Status: "estimated"}
	id := p.NextTraceID()
	if id < 0 {
		return result, fmt.Errorf("PMTU probe IDs exhausted")
	}
	if p.ids != nil {
		defer p.ids.releaseTrace(id)
	}
	buf := make([]byte, receiverBufferSize)
	// The IPv6 payload length excludes the 40-byte base header. In
	// particular, Linux loopback's 65536-byte MTU is not a jumbogram.
	const maxProbeMTU = receiverBufferSize + 40
	for seq := 1; seq <= 8; seq++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		mtu, err := c.PathMTU()
		if err != nil {
			return result, fmt.Errorf("query IPv6 path MTU: %w", err)
		}
		if mtu < 1280 {
			return result, fmt.Errorf("invalid IPv6 path MTU %d", mtu)
		}
		result.MTU, result.Payload = mtu, mtu-48
		if logf != nil {
			logf(fmt.Sprintf("[PMTU] %s: MTU=%d (OS estimate)", dst, mtu))
		}
		// Bound memory and wire size even for unusual jumbo loopback routes.
		payload := make([]byte, min(mtu, maxProbeMTU)-48)
		copy(payload, fmt.Sprintf("MPING-PMTU-%d-%d-%d", id, seq, time.Now().UnixNano()))
		message := icmp.Message{Type: ipv6.ICMPTypeEchoRequest, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}
		packet, err := message.Marshal(nil)
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
			for {
				if err := ctx.Err(); err != nil {
					return result, err
				}
				n, readErr := c.Read(buf)
				if readErr != nil {
					if ctx.Err() != nil {
						return result, ctx.Err()
					}
					if e, ok := readErr.(net.Error); !ok || !e.Timeout() {
						if !isMTUTooLarge(readErr) {
							return result, readErr
						}
					}
					break
				}
				msg, parseErr := icmp.ParseMessage(58, buf[:n])
				if parseErr != nil {
					continue
				}
				if echo, ok := msg.Body.(*icmp.Echo); msg.Type == ipv6.ICMPTypeEchoReply && ok && echo.ID == id && echo.Seq == seq && bytes.Equal(echo.Data, payload) {
					if mtu <= maxProbeMTU {
						result.Status = "confirmed"
					}
					if logf != nil {
						logf(fmt.Sprintf("[PMTU] %s: MTU=%d, payload=%d (%s)", dst, mtu, result.Payload, result.Status))
					}
					return result, nil
				}
				if msg.Type == ipv6.ICMPTypePacketTooBig {
					gotID, gotSeq, ok := extractEchoIDSeq(msg)
					if ok && gotID == id && gotSeq == seq {
						break // Kernel processes the PTB; query its updated estimate below.
					}
				}
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
		if next < 1280 {
			return result, fmt.Errorf("invalid IPv6 path MTU %d", next)
		}
		result.MTU, result.Payload = next, next-48
	}
	return result, nil
}
