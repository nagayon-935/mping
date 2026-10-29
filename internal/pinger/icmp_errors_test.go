package pinger

import (
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

func TestICMPErrorString(t *testing.T) {
	tests := []struct {
		name string
		typ  icmp.Type
		code int
		want string
	}{
		{"DstUnreach/Network", ipv4.ICMPTypeDestinationUnreachable, 0, "Destination Network Unreachable"},
		{"DstUnreach/Host", ipv4.ICMPTypeDestinationUnreachable, 1, "Destination Host Unreachable"},
		{"DstUnreach/Protocol", ipv4.ICMPTypeDestinationUnreachable, 2, "Destination Protocol Unreachable"},
		{"DstUnreach/Port", ipv4.ICMPTypeDestinationUnreachable, 3, "Destination Port Unreachable"},
		{"DstUnreach/FragNeeded", ipv4.ICMPTypeDestinationUnreachable, 4, "Fragmentation Needed"},
		{"DstUnreach/SourceRouteFailed", ipv4.ICMPTypeDestinationUnreachable, 5, "Source Route Failed"},
		{"DstUnreach/NetworkUnknown", ipv4.ICMPTypeDestinationUnreachable, 6, "Destination Network Unknown"},
		{"DstUnreach/HostUnknown", ipv4.ICMPTypeDestinationUnreachable, 7, "Destination Host Unknown"},
		{"DstUnreach/SourceHostIsolated", ipv4.ICMPTypeDestinationUnreachable, 8, "Source Host Isolated"},
		{"DstUnreach/NetworkProhibited", ipv4.ICMPTypeDestinationUnreachable, 9, "Network Administratively Prohibited"},
		{"DstUnreach/HostProhibited", ipv4.ICMPTypeDestinationUnreachable, 10, "Host Administratively Prohibited"},
		{"DstUnreach/NetworkToS", ipv4.ICMPTypeDestinationUnreachable, 11, "Network Unreachable for ToS"},
		{"DstUnreach/HostToS", ipv4.ICMPTypeDestinationUnreachable, 12, "Host Unreachable for ToS"},
		{"DstUnreach/CommProhibited", ipv4.ICMPTypeDestinationUnreachable, 13, "Communication Administratively Prohibited"},
		{"DstUnreach/PrecedenceViolation", ipv4.ICMPTypeDestinationUnreachable, 14, "Host Precedence Violation"},
		{"DstUnreach/PrecedenceCutoff", ipv4.ICMPTypeDestinationUnreachable, 15, "Precedence Cutoff in Effect"},
		{"DstUnreach/UnknownCode", ipv4.ICMPTypeDestinationUnreachable, 99, "Destination Unreachable"},

		{"TimeExceeded/TTL", ipv4.ICMPTypeTimeExceeded, 0, "Time Exceeded"},
		{"TimeExceeded/FragReassembly", ipv4.ICMPTypeTimeExceeded, 1, "Fragment Reassembly Time Exceeded"},
		{"TimeExceeded/UnknownCode", ipv4.ICMPTypeTimeExceeded, 99, "Time Exceeded"},

		{"ParamProblem/Default", ipv4.ICMPTypeParameterProblem, 0, "Parameter Problem"},
		{"ParamProblem/MissingOption", ipv4.ICMPTypeParameterProblem, 1, "Missing Required Option"},
		{"ParamProblem/BadLength", ipv4.ICMPTypeParameterProblem, 2, "Bad Length"},
		{"ParamProblem/UnknownCode", ipv4.ICMPTypeParameterProblem, 99, "Parameter Problem"},

		{"UnknownType", ipv4.ICMPTypeEchoReply, 0, "ICMP Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := icmpErrorString(tt.typ, tt.code); got != tt.want {
				t.Fatalf("icmpErrorString(%v, %d) = %q, want %q", tt.typ, tt.code, got, tt.want)
			}
		})
	}
}

// TestICMPv6ErrorString exercises icmpV6ErrorString across every RFC 4443
// type/code combination mping recognizes, including the Packet Too Big type
// (ipv6.ICMPTypePacketTooBig / type 2) that has no IPv4 equivalent, and the
// fallback path for unknown codes within a known type.
func TestICMPv6ErrorString(t *testing.T) {
	tests := []struct {
		name string
		typ  icmp.Type
		code int
		want string
	}{
		// Destination Unreachable (type 1)
		{"DstUnreach/NoRoute", ipv6.ICMPTypeDestinationUnreachable, 0, "No Route to Destination"},
		{"DstUnreach/AdminProhibited", ipv6.ICMPTypeDestinationUnreachable, 1, "Communication with Destination Administratively Prohibited"},
		{"DstUnreach/BeyondScope", ipv6.ICMPTypeDestinationUnreachable, 2, "Beyond Scope of Source Address"},
		{"DstUnreach/AddrUnreachable", ipv6.ICMPTypeDestinationUnreachable, 3, "Address Unreachable"},
		{"DstUnreach/PortUnreachable", ipv6.ICMPTypeDestinationUnreachable, 4, "Port Unreachable"},
		{"DstUnreach/PolicyFailure", ipv6.ICMPTypeDestinationUnreachable, 5, "Source Address Failed Ingress/Egress Policy"},
		{"DstUnreach/RejectRoute", ipv6.ICMPTypeDestinationUnreachable, 6, "Reject Route to Destination"},
		{"DstUnreach/UnknownCode", ipv6.ICMPTypeDestinationUnreachable, 99, "Destination Unreachable"},

		// Packet Too Big (type 2) - no IPv4 equivalent, code is always 0.
		{"PacketTooBig/Code0", ipv6.ICMPTypePacketTooBig, 0, "Packet Too Big"},
		{"PacketTooBig/UnknownCode", ipv6.ICMPTypePacketTooBig, 5, "Packet Too Big"},

		// Time Exceeded (type 3)
		{"TimeExceeded/HopLimit", ipv6.ICMPTypeTimeExceeded, 0, "Hop Limit Exceeded in Transit"},
		{"TimeExceeded/FragReassembly", ipv6.ICMPTypeTimeExceeded, 1, "Fragment Reassembly Time Exceeded"},
		{"TimeExceeded/UnknownCode", ipv6.ICMPTypeTimeExceeded, 99, "Time Exceeded"},

		// Parameter Problem (type 4)
		{"ParamProblem/ErroneousHeader", ipv6.ICMPTypeParameterProblem, 0, "Erroneous Header Field Encountered"},
		{"ParamProblem/UnrecognizedNextHeader", ipv6.ICMPTypeParameterProblem, 1, "Unrecognized Next Header Type Encountered"},
		{"ParamProblem/UnrecognizedOption", ipv6.ICMPTypeParameterProblem, 2, "Unrecognized IPv6 Option Encountered"},
		{"ParamProblem/UnknownCode", ipv6.ICMPTypeParameterProblem, 99, "Parameter Problem"},

		// Unknown type entirely.
		{"UnknownType", ipv6.ICMPTypeEchoReply, 0, "ICMPv6 Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := icmpV6ErrorString(tt.typ, tt.code); got != tt.want {
				t.Fatalf("icmpV6ErrorString(%v, %d) = %q, want %q", tt.typ, tt.code, got, tt.want)
			}
		})
	}
}

// TestPacketTooBigString verifies the MTU value reported by a router is
// embedded in the rendered error string, matching Apple ping6.c's
// "Packet too big mtu = %d" convention.
func TestPacketTooBigString(t *testing.T) {
	tests := []struct {
		mtu  int
		want string
	}{
		{1400, "Packet Too Big (MTU=1400)"},
		{1280, "Packet Too Big (MTU=1280)"},
		{0, "Packet Too Big (MTU=0)"},
	}
	for _, tt := range tests {
		if got := packetTooBigString(tt.mtu); got != tt.want {
			t.Fatalf("packetTooBigString(%d) = %q, want %q", tt.mtu, got, tt.want)
		}
	}
}

func TestLookupICMPCode(t *testing.T) {
	codes := map[int]string{0: "Zero", 1: "One"}
	tests := []struct {
		code int
		want string
	}{
		{0, "Zero"},
		{1, "One"},
		{99, "fallback"},
	}
	for _, tt := range tests {
		if got := lookupICMPCode(codes, tt.code, "fallback"); got != tt.want {
			t.Fatalf("lookupICMPCode(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestICMPErrorBodyData(t *testing.T) {
	tests := []struct {
		name   string
		msg    *icmp.Message
		wantOK bool
	}{
		{"DstUnreach", &icmp.Message{Body: &icmp.DstUnreach{Data: []byte("test")}}, true},
		{"TimeExceeded", &icmp.Message{Body: &icmp.TimeExceeded{Data: []byte("test")}}, true},
		{"ParamProb", &icmp.Message{Body: &icmp.ParamProb{Data: []byte("test")}}, true},
		{"PacketTooBig", &icmp.Message{Body: &icmp.PacketTooBig{MTU: 1400, Data: []byte("test")}}, true},
		{"Echo (unsupported)", &icmp.Message{Body: &icmp.Echo{Data: []byte("test")}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, ok := icmpErrorBodyData(tt.msg)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && string(data) != "test" {
				t.Fatalf("data = %q, want %q", data, "test")
			}
		})
	}
}

// echoBytes marshals an echo request of the given IP version, with the given
// id/seq.
func echoBytes(t *testing.T, v6 bool, id, seq int) []byte {
	t.Helper()
	typ := icmp.Type(ipv4.ICMPTypeEcho)
	if v6 {
		typ = ipv6.ICMPTypeEchoRequest
	}
	b, err := (&icmp.Message{Type: typ, Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("x")}}).Marshal(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ipHeader returns a minimal embedded original IP header: 20 bytes for IPv4
// (IHL=5), 40 for IPv6, with the given protocol / next-header value.
func ipHeader(v6 bool, proto byte) []byte {
	if v6 {
		h := make([]byte, 40)
		h[0], h[6] = 0x60, proto
		return h
	}
	h := make([]byte, 20)
	h[0], h[9] = 0x45, proto
	return h
}

func TestParseInnerEchoIDSeq(t *testing.T) {
	udpHdr := make([]byte, 8)
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	hdrWithIHL := func(b0 byte, n int) []byte {
		h := make([]byte, n)
		h[0] = b0
		return h
	}

	tests := []struct {
		name            string
		data            []byte
		wantOK          bool
		wantID, wantSeq int
	}{
		{"IPv4 ICMP", cat(ipHeader(false, 1), echoBytes(t, false, 42, 7)), true, 42, 7},
		{"IPv6 ICMPv6", cat(ipHeader(true, 58), echoBytes(t, true, 55, 9)), true, 55, 9},
		{"IPv4 UDP-wrapped ICMP", cat(ipHeader(false, 17), udpHdr, echoBytes(t, false, 99, 77)), true, 99, 77},
		{"IPv6 UDP-wrapped ICMPv6", cat(ipHeader(true, 17), udpHdr, echoBytes(t, true, 33, 44)), true, 33, 44},

		{"empty", []byte{}, false, 0, 0},
		{"single byte", []byte{0x40}, false, 0, 0},
		{"IPv4 header truncated", hdrWithIHL(0x45, 10), false, 0, 0},
		{"IPv4 IHL larger than data", hdrWithIHL(0x4A, 20), false, 0, 0},
		{"IPv4 IHL below minimum", hdrWithIHL(0x40, 20), false, 0, 0},
		{"IPv6 header truncated", hdrWithIHL(0x60, 20), false, 0, 0},
		{"unknown IP version", hdrWithIHL(0x50, 20), false, 0, 0},
		{"IPv4 UDP without room for UDP header", cat(ipHeader(false, 17), udpHdr[:4]), false, 0, 0},
		{"unsupported next protocol (TCP)", cat(ipHeader(false, 6), echoBytes(t, false, 1, 1)), false, 0, 0},
		{"inner ICMP too short to parse", cat(ipHeader(false, 1), []byte{0x00}), false, 0, 0},
		{"inner ICMP is not an echo", cat(ipHeader(false, 1), mustMarshal(t, &icmp.Message{
			Type: ipv4.ICMPTypeDestinationUnreachable, Body: &icmp.DstUnreach{Data: []byte("x")},
		})), false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, seq, ok := parseInnerEchoIDSeq(tt.data)
			if ok != tt.wantOK || id != tt.wantID || seq != tt.wantSeq {
				t.Fatalf("got id=%d seq=%d ok=%v, want id=%d seq=%d ok=%v", id, seq, ok, tt.wantID, tt.wantSeq, tt.wantOK)
			}
		})
	}
}

func mustMarshal(t *testing.T, m *icmp.Message) []byte {
	t.Helper()
	b, err := m.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestExtractEchoIDSeq covers both extraction strategies: parsing the
// embedded original IP+ICMP packet, and the fallback scan for the traceroute
// payload signature "TRC-" + ID (2B) + Seq (2B).
func TestExtractEchoIDSeq(t *testing.T) {
	v4Echo := ipPacketWithEcho(false, 12, 34)
	v6Echo := ipPacketWithEcho(true, 12, 34)

	tests := []struct {
		name            string
		typ             icmp.Type
		body            icmp.MessageBody
		wantOK          bool
		wantID, wantSeq int
	}{
		{"v4 destination unreachable", ipv4.ICMPTypeDestinationUnreachable, &icmp.DstUnreach{Data: v4Echo}, true, 12, 34},
		{"v6 destination unreachable", ipv6.ICMPTypeDestinationUnreachable, &icmp.DstUnreach{Data: v6Echo}, true, 12, 34},
		// Without a *icmp.PacketTooBig case in icmpErrorBodyData this returns
		// ok=false and the reply can never be routed back to its target.
		{"v6 packet too big", ipv6.ICMPTypePacketTooBig, &icmp.PacketTooBig{MTU: 1400, Data: v6Echo}, true, 12, 34},
		{"v4 time exceeded", ipv4.ICMPTypeTimeExceeded, &icmp.TimeExceeded{Data: v4Echo}, true, 12, 34},
		{"v4 parameter problem", ipv4.ICMPTypeParameterProblem, &icmp.ParamProb{Data: v4Echo}, true, 12, 34},

		// Payload that does not parse as an IP header falls back to the signature scan.
		{"TRC signature", ipv4.ICMPTypeDestinationUnreachable, &icmp.DstUnreach{Data: []byte("TRC-\x00\xab\x00\xcd")}, true, 0xAB, 0xCD},
		{"TRC signature at offset with trailing bytes", ipv4.ICMPTypeDestinationUnreachable,
			&icmp.DstUnreach{Data: []byte("\x00\x00TRC-\x01\x02\x03\x04\xff")}, true, 0x0102, 0x0304},
		{"TRC signature ends exactly at the end", ipv4.ICMPTypeDestinationUnreachable,
			&icmp.DstUnreach{Data: []byte("zzTRC-\x00\x07\x00\x09")}, true, 7, 9},
		{"first TRC occurrence is the only candidate", ipv4.ICMPTypeDestinationUnreachable,
			&icmp.DstUnreach{Data: []byte("TRC-\x00\x01\x00\x02TRC-\x00\x03\x00\x04")}, true, 1, 2},
		{"TRC signature with 3 trailing bytes", ipv4.ICMPTypeDestinationUnreachable,
			&icmp.DstUnreach{Data: []byte("TRC-\x00\x07\x00")}, false, 0, 0},
		{"TRC signature without trailing bytes", ipv4.ICMPTypeDestinationUnreachable,
			&icmp.DstUnreach{Data: []byte("xxxxTRC-")}, false, 0, 0},
		{"no signature and no IP header", ipv4.ICMPTypeDestinationUnreachable,
			&icmp.DstUnreach{Data: []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}}, false, 0, 0},

		{"message type without an error body", ipv4.ICMPTypeEchoReply, nil, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, seq, ok := extractEchoIDSeq(&icmp.Message{Type: tt.typ, Body: tt.body})
			if ok != tt.wantOK || id != tt.wantID || seq != tt.wantSeq {
				t.Fatalf("got id=%d seq=%d ok=%v, want id=%d seq=%d ok=%v", id, seq, ok, tt.wantID, tt.wantSeq, tt.wantOK)
			}
		})
	}
}
