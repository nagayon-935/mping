package pinger

import (
	"fmt"
	"net"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ── extractSrcIP ────────────────────────────────────────────────────────────

func TestExtractSrcIP(t *testing.T) {
	tests := []struct {
		name string
		src  net.Addr
		want string
	}{
		{"nil", nil, ""},
		{"IPAddr", &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}, "10.0.0.1"},
		{"UDPAddr", &net.UDPAddr{IP: net.IPv4(192, 168, 1, 2), Port: 53}, "192.168.1.2"},
		{"IPv6 IPAddr", &net.IPAddr{IP: net.ParseIP("2001:db8::1")}, "2001:db8::1"},
		{"other addr falls back to String", &net.TCPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 80}, "1.1.1.1:80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractSrcIP(tt.src); got != tt.want {
				t.Errorf("extractSrcIP(%v) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// ── acceptHopPacket: DestinationUnreachable ─────────────────────────────────

// buildDestUnreachableMsg builds an ICMPv4 Destination Unreachable wrapping an
// Echo with the given id and seq.
func buildDestUnreachableMsg(id, seq int) []byte {
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

func TestAcceptHopPacket_DestinationUnreachable(t *testing.T) {
	traceID := 0x4321
	ttl := 7

	raw := buildDestUnreachableMsg(traceID, ttl)
	msg, err := icmp.ParseMessage(1, raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	src := &net.IPAddr{IP: net.IPv4(203, 0, 113, 9)}
	reply, matched := acceptHopPacket(msg, src, traceID, ttl)
	if !matched {
		t.Fatal("expected match for Destination Unreachable")
	}
	if !reply.ReachedDest {
		t.Error("Destination Unreachable should set ReachedDest")
	}
	if !reply.Responded {
		t.Error("expected Responded=true")
	}
	if reply.SrcIP != "203.0.113.9" {
		t.Errorf("SrcIP: want 203.0.113.9, got %q", reply.SrcIP)
	}
}

func TestAcceptHopPacket_UnhandledType(t *testing.T) {
	// An Echo request (not a reply) is not one of the handled cases.
	raw, _ := (&icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{ID: 1, Seq: 1},
	}).Marshal(nil)
	msg, _ := icmp.ParseMessage(1, raw)
	if _, matched := acceptHopPacket(msg, nil, 1, 1); matched {
		t.Error("echo request should not be accepted as a hop reply")
	}
}

// ── GetASNInfoFor ────────────────────────────────────────────────────────────

func TestGetASNFor_Disabled(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{AsnEnabled: false})
	if got := p.GetASNInfoFor("8.8.8.8").Number; got != "" {
		t.Errorf("GetASNInfoFor with ASN disabled = %q, want empty", got)
	}
	if got := p.GetASNInfoFor("8.8.8.8"); got != (ASNInfo{}) {
		t.Errorf("GetASNInfoFor with ASN disabled = %+v, want zero value", got)
	}
}

func TestGetASNFor_Enabled(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		switch {
		case contains(query, "origin"):
			return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
		case contains(query, "15169.asn"):
			return []string{"15169 | GOOGLE - Google LLC, US | 1992-12-01"}, nil
		}
		return nil, fmt.Errorf("not found")
	}
	p := NewPingerWithOptions(nil, Options{AsnEnabled: true, LookupTXT: mockLookup})

	if got := p.GetASNInfoFor("8.8.8.8").Number; got != "AS15169" {
		t.Errorf("GetASNInfoFor = %q, want AS15169", got)
	}
	info := p.GetASNInfoFor("8.8.8.8")
	if info.Number != "AS15169" {
		t.Errorf("GetASNInfoFor.Number = %q, want AS15169", info.Number)
	}
	if info.Country != "US" {
		t.Errorf("GetASNInfoFor.Country = %q, want US", info.Country)
	}
}

// contains is a tiny helper to avoid importing strings in this file.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
