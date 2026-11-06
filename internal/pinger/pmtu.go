package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ErrPMTUNoReply means the baseline echo did not confirm a reachable path.
var ErrPMTUNoReply = errors.New("PMTU baseline received no echo reply")

// canSendPayloadFn is a variable holding canSendPayload to allow overriding in tests.
var canSendPayloadFn = func(ctx context.Context, p *Pinger, dst *net.IPAddr, payloadLen, id int) (bool, string, error) {
	return p.canSendPayload(ctx, dst, payloadLen, id)
}

// pmtuRawConn is the subset of *ipv4.RawConn that canSendPayload uses.
type pmtuRawConn interface {
	WriteTo(h *ipv4.Header, p []byte, cm *ipv4.ControlMessage) error
	SetReadDeadline(t time.Time) error
	ReadFrom(b []byte) (*ipv4.Header, []byte, *ipv4.ControlMessage, error)
}

// newRawConnFn wraps canSendPayload's socket for header-included writes; a
// variable so tests can substitute a fake, since a real one needs a raw socket.
var newRawConnFn = func(c net.PacketConn) (pmtuRawConn, error) {
	rc, err := ipv4.NewRawConn(c)
	if err != nil {
		return nil, err
	}
	return rc, nil
}

// Use the already selected destination throughout the IPv4 search. In
// particular, never perform a second DNS lookup after deciding the family.
func (p *Pinger) discoverIPv4MaxPayload(ctx context.Context, dstAddr *net.IPAddr, start, minPayload int, logf func(string)) (int, string, error) {
	if start <= 0 {
		return 0, "", fmt.Errorf("start MTU must be > 0")
	}
	low := max(minPayload, 0)
	if low > start {
		low = start
	}
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}
	id, release := p.acquireTraceID()
	if id < 0 {
		return 0, "", fmt.Errorf("PMTU probe IDs exhausted")
	}
	defer release()
	ok, hop, err := canSendPayloadFn(ctx, p, dstAddr, low, id)
	if err != nil {
		return 0, hop, err
	}
	if err := ctx.Err(); err != nil {
		return 0, hop, err
	}
	if !ok {
		if hop != "" {
			return 0, hop, fmt.Errorf("PMTU %s (bottleneck %s): %w", dstAddr, hop, ErrPMTUNoReply)
		}
		return 0, "", fmt.Errorf("PMTU %s: %w", dstAddr, ErrPMTUNoReply)
	}
	high := start
	var bottleneckIP string
	for low < high {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		default:
		}
		mid := (low + high + 1) / 2
		ok, hopIP, err := canSendPayloadFn(ctx, p, dstAddr, mid, id)
		if err != nil {
			return 0, "", err
		}
		if ok {
			if logf != nil {
				logf(fmt.Sprintf("[PMTU] payload=%d OK", mid))
			}
			low = mid
		} else {
			if hopIP != "" {
				bottleneckIP = hopIP
			}
			if logf != nil {
				if bottleneckIP != "" {
					logf(fmt.Sprintf("[PMTU] payload=%d FAIL (mtu mismatch: %s)", mid, bottleneckIP))
				} else {
					logf(fmt.Sprintf("[PMTU] payload=%d FAIL", mid))
				}
			}
			high = mid - 1
		}
	}
	if bottleneckIP != "" && logf != nil {
		logf(fmt.Sprintf("[PMTU] mtu mismatch at: %s", bottleneckIP))
	}
	return low, bottleneckIP, nil
}

// canSendPayload sends a single ICMP Echo with the given payload length and
// DF bit set. Returns true if an Echo Reply is received, false if the packet
// is too large (EMSGSIZE or ICMP Fragmentation Needed), the bottleneck router
// IP if an ICMP Fragmentation Needed response was received, and an error for
// unexpected failures.
func (p *Pinger) canSendPayload(ctx context.Context, dstAddr *net.IPAddr, payloadLen, id int) (bool, string, error) {
	if payloadLen < 0 {
		payloadLen = 0
	}

	payload := buildPayload(payloadLen)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  0,
			Data: payload,
		},
	}
	b, err := marshalProbe(&msg)
	if err != nil {
		return false, "", err
	}

	c, source, err := p.openIPv4PMTUSocket()
	if err != nil {
		return false, "", err
	}
	defer c.Close()
	bindToInterfaceFn(c, p.Interface, false)
	defer closeOnCancel(ctx, func() { _ = c.Close() })()

	rc, err := newRawConnFn(c)
	if err != nil {
		return false, "", err
	}

	// On macOS, setting the DF bit in the IP header via IP_HDRINCL is not
	// sufficient — the kernel silently fragments the packet anyway. We must
	// also set the IP_DONTFRAG socket option to prevent this.
	setSocketDontFragment(c)

	h := &ipv4.Header{
		Version:  4,
		Len:      ipv4.HeaderLen,
		TotalLen: ipv4.HeaderLen + len(b),
		TTL:      64,
		Protocol: protoICMP,
		Dst:      dstAddr.IP,
		Flags:    ipv4.DontFragment,
	}
	if source != "" {
		h.Src = net.ParseIP(source)
	}

	if err := rc.WriteTo(h, b, nil); err != nil {
		if isMTUTooLarge(err) {
			return false, "", nil
		}
		return false, "", err
	}

	return waitIPv4PMTUReply(rc, id)
}

// openIPv4PMTUSocket selects the source and opens a socket for header-included
// DF probes. The caller owns interface binding, cancellation, and closing.
func (p *Pinger) openIPv4PMTUSocket() (net.PacketConn, string, error) {
	// Header-included IPv4 probes bind both the requested interface and its
	// IPv4 source on every OS, measuring the same path as regular ping.
	source := p.Source
	if source == "" && p.Interface != "" {
		var err error
		source, err = p.interfaceSource(false)
		if err != nil {
			return nil, "", err
		}
	}
	c, err := p.listenICMPSource(false, source)
	return c, source, err
}

// waitIPv4PMTUReply accepts a matching echo or fragmentation-needed response.
// All read errors remain equivalent to no reply.
func waitIPv4PMTUReply(rc pmtuRawConn, id int) (bool, string, error) {
	deadline := time.Now().Add(pmtuProbeTimeout)
	// The receive buffer must be large enough to hold a full ICMP Echo Reply
	// for any probe size we send. Using probeBufferSize (1500) would cause
	// truncated reads for large payloads, making the ID/Seq still parseable
	// and producing false-positive OK results.
	buf := make([]byte, receiverBufferSize)
	for time.Now().Before(deadline) {
		_ = rc.SetReadDeadline(deadline)
		hdr, pld, _, err := rc.ReadFrom(buf)
		if err != nil {
			// A read timeout or any other read error (e.g. socket closed) is
			// treated as no reply.
			return false, "", nil
		}
		parsed, err := icmp.ParseMessage(protoICMP, pld)
		if err != nil {
			continue
		}
		switch parsed.Type {
		case ipv4.ICMPTypeEchoReply:
			if echo, ok := parsed.Body.(*icmp.Echo); ok {
				if echo.ID == id && echo.Seq == 0 {
					return true, "", nil
				}
			}
		case ipv4.ICMPTypeDestinationUnreachable:
			if parsed.Code == 4 { // Fragmentation Needed
				replyID, seq, ok := extractEchoIDSeq(parsed)
				if ok && replyID == id && seq == 0 {
					var srcIP string
					if hdr != nil && hdr.Src != nil {
						srcIP = hdr.Src.String()
					}
					return false, srcIP, nil
				}
			}
		}
	}
	// Deadline exceeded with no matching reply.
	return false, "", nil
}

func isMTUTooLarge(err error) bool {
	if errors.Is(err, syscall.EMSGSIZE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "message too long") || strings.Contains(msg, "EMSGSIZE")
}
