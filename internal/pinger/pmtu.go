package pinger

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// canSendPayloadFn is a variable holding canSendPayload to allow overriding in tests.
var canSendPayloadFn = (*Pinger).canSendPayload

// DiscoverMaxPayload performs a binary search to find the maximum ICMP payload
// size that can be sent to dest without fragmentation (DF flag set).
// start is the upper bound to probe; min is the lower bound (usually the
// configured packet size). IPv6 destinations are not supported.
func (p *Pinger) DiscoverMaxPayload(dest string, start int, min int, logf func(string)) (int, error) {
	if dest == "" {
		return 0, fmt.Errorf("destination is empty")
	}
	if start <= 0 {
		return 0, fmt.Errorf("start MTU must be > 0")
	}
	if min < 0 {
		min = 0
	}

	dstAddr, err := p.resolveIPAddr("ip", dest)
	if err != nil {
		return 0, fmt.Errorf("resolve %s: %w", dest, err)
	}

	// PMTU currently only supported for IPv4
	if dstAddr.IP.To4() == nil {
		return p.Size, fmt.Errorf("PMTU discovery not supported for IPv6")
	}

	low := min
	if low > start {
		low = start
	}
	high := start
	for low < high {
		mid := (low + high + 1) / 2
		ok, err := canSendPayloadFn(p, dstAddr, mid)
		if err != nil {
			return 0, err
		}
		if ok {
			if logf != nil {
				logf(fmt.Sprintf("[PMTU] payload=%d OK", mid))
			}
			low = mid
		} else {
			if logf != nil {
				logf(fmt.Sprintf("[PMTU] payload=%d FAIL", mid))
			}
			high = mid - 1
		}
	}
	return low, nil
}

// canSendPayload sends a single ICMP Echo with the given payload length and
// DF bit set. Returns true if an Echo Reply is received, false if the packet
// is too large (EMSGSIZE or ICMP Fragmentation Needed), and an error for
// unexpected failures.
func (p *Pinger) canSendPayload(dstAddr *net.IPAddr, payloadLen int) (bool, error) {
	if payloadLen < 0 {
		payloadLen = 0
	}

	payload := buildPayload(payloadLen)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   p.baseID & 0xffff,
			Seq:  0,
			Data: payload,
		},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return false, err
	}

	bindAddr := "0.0.0.0"
	if p.Source != "" {
		bindAddr = p.Source
	}

	c, err := p.listenPacket("ip4:icmp", bindAddr)
	if err != nil {
		return false, err
	}
	defer c.Close()

	rc, err := ipv4.NewRawConn(c)
	if err != nil {
		return false, err
	}

	h := &ipv4.Header{
		Version:  4,
		Len:      ipv4.HeaderLen,
		TotalLen: ipv4.HeaderLen + len(b),
		TTL:      64,
		Protocol: 1,
		Dst:      dstAddr.IP,
		Flags:    ipv4.DontFragment,
	}
	if p.Source != "" {
		h.Src = net.ParseIP(p.Source)
	}

	if err := rc.WriteTo(h, b, nil); err != nil {
		if isMTUTooLarge(err) {
			return false, nil
		}
		return false, err
	}

	deadline := time.Now().Add(pmtuProbeTimeout)
	buf := make([]byte, probeBufferSize)
	for time.Now().Before(deadline) {
		_ = rc.SetReadDeadline(deadline)
		_, pld, _, err := rc.ReadFrom(buf)
		if err != nil {
			if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
				return false, nil
			}
			// Non-timeout read errors (e.g. socket closed) are treated as no reply.
			return false, nil
		}
		parsed, err := icmp.ParseMessage(1, pld)
		if err != nil {
			continue
		}
		switch parsed.Type {
		case ipv4.ICMPTypeEchoReply:
			if echo, ok := parsed.Body.(*icmp.Echo); ok {
				if echo.ID == (p.baseID&0xffff) && echo.Seq == 0 {
					return true, nil
				}
			}
		case ipv4.ICMPTypeDestinationUnreachable:
			if parsed.Code == 4 { // Fragmentation Needed
				return false, nil
			}
		}
	}
	// Deadline exceeded with no matching reply.
	return false, nil
}

func isMTUTooLarge(err error) bool {
	if errors.Is(err, syscall.EMSGSIZE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "message too long") || strings.Contains(msg, "EMSGSIZE")
}
