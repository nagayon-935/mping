package pinger

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

func icmpErrorString(typ icmp.Type, code int) string {
	switch typ {
	case ipv4.ICMPTypeDestinationUnreachable:
		return lookupICMPCode(destUnreachCodes, code, "Destination Unreachable")
	case ipv4.ICMPTypeTimeExceeded:
		return lookupICMPCode(timeExceededCodes, code, "Time Exceeded")
	case ipv4.ICMPTypeParameterProblem:
		return lookupICMPCode(paramProblemCodes, code, "Parameter Problem")
	default:
		return "ICMP Error"
	}
}

// icmpV6ErrorString renders a type/code pair into a human-readable string.
// Note that unlike IPv4 - where "Fragmentation Needed" is DstUnreach code 4 -
// IPv6 has no equivalent DstUnreach code: Packet Too Big is its own,
// independent ICMPv6 type (type 2, RFC 4443 section 3.2). Its code is always
// 0, so the useful diagnostic (the next-hop MTU) is carried in the message
// body rather than the code; callers that have the full *icmp.Message should
// prefer packetTooBigString(body.MTU) for that reason (see handleICMPError).
func icmpV6ErrorString(typ icmp.Type, code int) string {
	switch typ {
	case ipv6.ICMPTypeDestinationUnreachable:
		return lookupICMPCode(destUnreachV6Codes, code, "Destination Unreachable")
	case ipv6.ICMPTypePacketTooBig:
		return "Packet Too Big"
	case ipv6.ICMPTypeTimeExceeded:
		return lookupICMPCode(timeExceededV6Codes, code, "Time Exceeded")
	case ipv6.ICMPTypeParameterProblem:
		return lookupICMPCode(paramProblemV6Codes, code, "Parameter Problem")
	default:
		return "ICMPv6 Error"
	}
}

var destUnreachCodes = map[int]string{
	0:  "Destination Network Unreachable",
	1:  "Destination Host Unreachable",
	2:  "Destination Protocol Unreachable",
	3:  "Destination Port Unreachable",
	4:  "Fragmentation Needed",
	5:  "Source Route Failed",
	6:  "Destination Network Unknown",
	7:  "Destination Host Unknown",
	8:  "Source Host Isolated",
	9:  "Network Administratively Prohibited",
	10: "Host Administratively Prohibited",
	11: "Network Unreachable for ToS",
	12: "Host Unreachable for ToS",
	13: "Communication Administratively Prohibited",
	14: "Host Precedence Violation",
	15: "Precedence Cutoff in Effect",
}

// destUnreachV6Codes maps RFC 4443 section 3.1 Destination Unreachable codes.
var destUnreachV6Codes = map[int]string{
	0: "No Route to Destination",
	1: "Communication with Destination Administratively Prohibited",
	2: "Beyond Scope of Source Address",
	3: "Address Unreachable",
	4: "Port Unreachable",
	5: "Source Address Failed Ingress/Egress Policy",
	6: "Reject Route to Destination",
}

var timeExceededCodes = map[int]string{
	0: "Time Exceeded",
	1: "Fragment Reassembly Time Exceeded",
}

// timeExceededV6Codes maps RFC 4443 section 3.3 Time Exceeded codes. Code 0
// means the hop limit was exceeded in transit, distinct from IPv4's generic
// "Time Exceeded" wording for the same code.
var timeExceededV6Codes = map[int]string{
	0: "Hop Limit Exceeded in Transit",
	1: "Fragment Reassembly Time Exceeded",
}

var paramProblemCodes = map[int]string{
	0: "Parameter Problem",
	1: "Missing Required Option",
	2: "Bad Length",
}

// paramProblemV6Codes maps RFC 4443 section 3.4 Parameter Problem codes.
// These do not correspond to IPv4's ICMPTypeParameterProblem codes, which
// index a pointer into the offending packet rather than naming a category of
// error - IPv6 code 0/1/2 name a category directly.
var paramProblemV6Codes = map[int]string{
	0: "Erroneous Header Field Encountered",
	1: "Unrecognized Next Header Type Encountered",
	2: "Unrecognized IPv6 Option Encountered",
}

func lookupICMPCode(codes map[int]string, code int, fallback string) string {
	if s, ok := codes[code]; ok {
		return s
	}
	return fallback
}

// packetTooBigString renders an ICMPv6 Packet Too Big message (RFC 4443
// section 3.2), including the next-hop MTU reported by the router so MTU and
// fragmentation issues are diagnosable straight from the TUI instead of
// showing up as a generic timeout. Mirrors Apple's ping6.c, which prints
// "Packet too big mtu = %d".
func packetTooBigString(mtu int) string {
	return fmt.Sprintf("Packet Too Big (MTU=%d)", mtu)
}

// icmpErrorBodyData extracts the embedded original packet data from an ICMP error body.
func icmpErrorBodyData(msg *icmp.Message) ([]byte, bool) {
	switch body := msg.Body.(type) {
	case *icmp.DstUnreach:
		return body.Data, true
	case *icmp.TimeExceeded:
		return body.Data, true
	case *icmp.ParamProb:
		return body.Data, true
	case *icmp.PacketTooBig:
		return body.Data, true
	default:
		return nil, false
	}
}

// extractEchoIDSeq extracts the ICMP echo ID and sequence number from an ICMP
// error message (Time Exceeded, Destination Unreachable, Parameter Problem).
// It first tries parsing the embedded original IP+ICMP headers, then falls back
// to scanning for the traceSignature pattern in the payload.
func extractEchoIDSeq(msg *icmp.Message) (int, int, bool) {
	data, ok := icmpErrorBodyData(msg)
	if !ok {
		return 0, 0, false
	}

	if id, seq, ok := parseInnerEchoIDSeq(data); ok {
		return id, seq, true
	}

	// Fallback: Pattern matching for traceSignature + ID (2B) + Seq (2B).
	// Only the first occurrence can fit: if it leaves fewer than 8 bytes, every
	// later occurrence does too.
	if i := bytes.Index(data, []byte(traceSignature)); i >= 0 && i+8 <= len(data) {
		return int(binary.BigEndian.Uint16(data[i+4:])), int(binary.BigEndian.Uint16(data[i+6:])), true
	}
	return 0, 0, false
}

// parseInnerEchoIDSeq parses the embedded original packet data from an ICMP
// error body and returns the ICMP echo ID and sequence number.
func parseInnerEchoIDSeq(data []byte) (int, int, bool) {
	if len(data) < 1 {
		return 0, 0, false
	}

	// Pick the ICMP protocol number (also used as the IP next-header value for
	// a directly embedded ICMP message), the IP header length and the inner
	// next-header from the embedded original IP header.
	var icmpProto, headerLen, nextHeader int
	switch data[0] >> 4 {
	case 4:
		if len(data) < 20 {
			return 0, 0, false
		}
		ihl := int(data[0]&0x0f) * 4
		if ihl < 20 || len(data) < ihl {
			return 0, 0, false
		}
		icmpProto, headerLen, nextHeader = 1, ihl, int(data[9])
	case 6:
		// IPv6 header is 40 bytes.
		const ipv6HeaderLen = 40
		if len(data) < ipv6HeaderLen {
			return 0, 0, false
		}
		icmpProto, headerLen, nextHeader = 58, ipv6HeaderLen, int(data[6]) // Next Header
	default:
		return 0, 0, false
	}

	// The inner packet could be ICMP or UDP (protocol 17) if we sent it via a
	// udp socket.
	innerData := data[headerLen:]
	switch nextHeader {
	case icmpProto:
	case 17: // UDP
		// If we sent ICMP over a UDP socket (non-privileged), the original
		// packet will have a UDP header (8 bytes). Skip it and try to parse the
		// payload as ICMP.
		if len(innerData) < 8 {
			return 0, 0, false
		}
		innerData = innerData[8:]
	default:
		return 0, 0, false
	}

	inner, err := icmp.ParseMessage(icmpProto, innerData)
	if err != nil {
		return 0, 0, false
	}
	if echo, ok := inner.Body.(*icmp.Echo); ok {
		return echo.ID, echo.Seq, true
	}
	return 0, 0, false
}
