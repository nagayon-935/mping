package pinger

import (
	"net"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// buildDestUnreachMsg builds an ICMPv4 Destination Unreachable wrapping an Echo.
func buildDestUnreachMsg(id, seq int) []byte {
	inner, _ := (&icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{ID: id, Seq: seq, Data: make([]byte, 8)},
	}).Marshal(nil)
	unused := make([]byte, 4)
	ipHdr := make([]byte, 20)
	ipHdr[0] = 0x45
	ipHdr[9] = 1
	payload := append(unused, append(ipHdr, inner[:8]...)...)
	msg, _ := (&icmp.Message{
		Type: ipv4.ICMPTypeDestinationUnreachable, Code: 3,
		Body: &icmp.RawBody{Data: payload},
	}).Marshal(nil)
	return msg
}

var loopbackAddr = &net.IPAddr{IP: net.ParseIP("127.0.0.1")}

// ── acceptParisPacket ─────────────────────────────────────────────────────────

func TestAcceptParisPacket_EchoReplyAccepted(t *testing.T) {
	const traceID = 0x1234
	raw := buildEchoReplyMsg(traceID, parisSeq)
	msg, _ := icmp.ParseMessage(1, raw)

	srcIP, reached, accepted := acceptParisPacket(msg, loopbackAddr, traceID)
	if !accepted {
		t.Fatal("want accepted=true for matching EchoReply")
	}
	if !reached {
		t.Error("want reachedDest=true for EchoReply")
	}
	if srcIP != "127.0.0.1" {
		t.Errorf("want srcIP=127.0.0.1, got %q", srcIP)
	}
}

func TestAcceptParisPacket_TimeExceededAccepted(t *testing.T) {
	const traceID = 0x5678
	raw := buildTimeExceededMsg(traceID, parisSeq)
	msg, _ := icmp.ParseMessage(1, raw)

	_, reached, accepted := acceptParisPacket(msg, loopbackAddr, traceID)
	if !accepted {
		t.Fatal("want accepted=true for matching TimeExceeded")
	}
	if reached {
		t.Error("want reachedDest=false for TimeExceeded (intermediate hop)")
	}
}

func TestAcceptParisPacket_DestUnreachableAccepted(t *testing.T) {
	const traceID = 0xABCD
	raw := buildDestUnreachMsg(traceID, parisSeq)
	msg, _ := icmp.ParseMessage(1, raw)

	_, reached, accepted := acceptParisPacket(msg, loopbackAddr, traceID)
	if !accepted {
		t.Fatal("want accepted=true for matching DestUnreachable")
	}
	if !reached {
		t.Error("want reachedDest=true for DestUnreachable")
	}
}

func TestAcceptParisPacket_WrongIDRejected(t *testing.T) {
	const traceID = 0x1111
	const wrongID = 0x2222
	raw := buildEchoReplyMsg(wrongID, parisSeq)
	msg, _ := icmp.ParseMessage(1, raw)

	_, _, accepted := acceptParisPacket(msg, loopbackAddr, traceID)
	if accepted {
		t.Fatal("want accepted=false for wrong ID")
	}
}

func TestAcceptParisPacket_NonZeroSeqRejected(t *testing.T) {
	// Standard traceroute uses Seq=ttl (non-zero). Paris should not accept these.
	const traceID = 0x9999
	raw := buildTimeExceededMsg(traceID, 5) // seq=5 (standard traceroute TTL=5)
	msg, _ := icmp.ParseMessage(1, raw)

	_, _, accepted := acceptParisPacket(msg, loopbackAddr, traceID)
	if accepted {
		t.Fatal("want accepted=false for non-zero Seq (standard traceroute probe)")
	}
}

func TestAcceptParisPacket_EchoReplyWrongSeqRejected(t *testing.T) {
	const traceID = 0xBEEF
	raw := buildEchoReplyMsg(traceID, 3) // seq=3, not parisSeq
	msg, _ := icmp.ParseMessage(1, raw)

	_, _, accepted := acceptParisPacket(msg, loopbackAddr, traceID)
	if accepted {
		t.Fatal("want accepted=false for EchoReply with wrong Seq")
	}
}

func TestAcceptParisPacket_IPv6TimeExceededAccepted(t *testing.T) {
	const traceID = 0x4321
	// Build ICMPv6 Time Exceeded wrapping an ICMPv6 Echo with parisSeq.
	inner, _ := (&icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest, Code: 0,
		Body: &icmp.Echo{ID: traceID, Seq: parisSeq, Data: make([]byte, 8)},
	}).Marshal(nil)
	// IPv6 header (40 bytes): version/TC/FL(4) + payload length(2) + next header(1) + hop limit(1) + src(16) + dst(16)
	ip6Hdr := make([]byte, 40)
	ip6Hdr[0] = 0x60 // version=6
	ip6Hdr[6] = 58   // next header = ICMPv6
	// ICMPv6 Time Exceeded body = 4-byte unused + IPv6 header + inner ICMPv6.
	// The icmp library strips the 4-byte unused field when parsing TimeExceeded,
	// so RawBody.Data must include it as a prefix.
	unused := make([]byte, 4)
	payload := append(append(unused, ip6Hdr...), inner[:8]...)
	raw, _ := (&icmp.Message{
		Type: ipv6.ICMPTypeTimeExceeded, Code: 0,
		Body: &icmp.RawBody{Data: payload},
	}).Marshal(nil)
	msg, _ := icmp.ParseMessage(58, raw)

	src := &net.IPAddr{IP: net.ParseIP("::1")}
	_, reached, accepted := acceptParisPacket(msg, src, traceID)
	if !accepted {
		t.Fatal("want accepted=true for IPv6 TimeExceeded")
	}
	if reached {
		t.Error("want reachedDest=false for IPv6 TimeExceeded")
	}
}
