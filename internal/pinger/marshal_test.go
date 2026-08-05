package pinger

import (
	"bytes"
	"strconv"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// icmpv6Proto / icmpv4Proto are the IANA protocol numbers used by
// icmp.ParseMessage. Mirrors the literals already used in pinger.go
// (see the protocol field on the receiver config).
const (
	icmpv4Proto = 1
	icmpv6Proto = 58
)

// buildTestPayload returns a payload of the given size filled with a
// byte pattern that makes any corruption (e.g. a 4-byte length field
// stomped into the middle of it) trivially visible in a diff.
func buildTestPayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251) // avoid an all-zero/constant pattern
	}
	return payload
}

func newEchoMessage(msgType icmp.Type, size int) *icmp.Message {
	return &icmp.Message{
		Type: msgType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   1,
			Seq:  1,
			Data: buildTestPayload(size),
		},
	}
}

// TestMarshalProbe_IPv6_MatchesNilMarshal verifies marshalProbe produces
// byte-for-byte the same output as msg.Marshal(nil) for IPv6, across
// sizes that straddle every boundary that matters:
//   - the old size<=1400 vs size>1400 branch point
//   - offset 32 in the marshaled buffer, where a non-nil-but-empty
//     pseudo header causes x/net to stomp a 4-byte length field
//     (offset 32 corresponds to payload byte 24, since the ICMP echo
//     header occupies the first 8 bytes: type/code/checksum + id/seq)
func TestMarshalProbe_IPv6_MatchesNilMarshal(t *testing.T) {
	sizes := []int{0, 1, 20, 23, 24, 27, 28, 32, 33, 56, 100, 1399, 1400, 1401, 1500, 2000}

	for _, size := range sizes {
		t.Run("size_"+strconv.Itoa(size), func(t *testing.T) {
			msg := newEchoMessage(ipv6.ICMPTypeEchoRequest, size)

			got, err := marshalProbe(msg)
			if err != nil {
				t.Fatalf("marshalProbe returned error: %v", err)
			}

			want, err := msg.Marshal(nil)
			if err != nil {
				t.Fatalf("msg.Marshal(nil) returned error: %v", err)
			}

			if !bytes.Equal(got, want) {
				t.Fatalf("marshalProbe output diverges from Marshal(nil) at size=%d\n got=% x\nwant=% x", size, got, want)
			}
		})
	}
}

// TestMarshalProbe_IPv6_PayloadIntegrity is the core regression test for
// the bug: it round-trips a known payload through marshalProbe and
// verifies every byte survives. Before the fix, sizes >= 28 corrupt
// payload bytes 24-27 because x/net's icmp.Message.Marshal writes a
// 4-byte pseudo-header length field at buffer offset 32 whenever psh is
// non-nil -- even when psh is the empty-but-non-nil buf[:0] slice this
// package used to pass in.
func TestMarshalProbe_IPv6_PayloadIntegrity(t *testing.T) {
	sizes := []int{0, 8, 20, 23, 24, 27, 28, 29, 32, 56, 100, 1400, 1401, 1500}

	for _, size := range sizes {
		t.Run("size_"+strconv.Itoa(size), func(t *testing.T) {
			payload := buildTestPayload(size)
			msg := &icmp.Message{
				Type: ipv6.ICMPTypeEchoRequest,
				Code: 0,
				Body: &icmp.Echo{ID: 1, Seq: 1, Data: payload},
			}

			b, err := marshalProbe(msg)
			if err != nil {
				t.Fatalf("marshalProbe returned error: %v", err)
			}

			parsed, err := icmp.ParseMessage(icmpv6Proto, b)
			if err != nil {
				t.Fatalf("icmp.ParseMessage failed: %v", err)
			}
			echo, ok := parsed.Body.(*icmp.Echo)
			if !ok {
				t.Fatalf("parsed body is %T, want *icmp.Echo", parsed.Body)
			}

			if !bytes.Equal(echo.Data, payload) {
				t.Fatalf("payload corrupted for size=%d\n got=% x\nwant=% x", size, echo.Data, payload)
			}
		})
	}
}

// TestMarshalProbe_IPv6_ChecksumComputedByKernel verifies that, post-fix,
// marshalProbe always leaves the checksum field zeroed for IPv6 (psh ==
// nil tells x/net to let the kernel compute it on send, per the
// icmp.Message.Marshal contract). This documents the intended contract
// so a future change can't silently reintroduce a non-nil psh.
func TestMarshalProbe_IPv6_ChecksumComputedByKernel(t *testing.T) {
	msg := newEchoMessage(ipv6.ICMPTypeEchoRequest, 56)

	b, err := marshalProbe(msg)
	if err != nil {
		t.Fatalf("marshalProbe returned error: %v", err)
	}

	if len(b) < 4 {
		t.Fatalf("marshaled message too short: %d bytes", len(b))
	}
	if b[2] != 0 || b[3] != 0 {
		t.Fatalf("checksum field not zero, got % x; expected the kernel to fill it in for IPv6", b[2:4])
	}
}

// TestMarshalProbe_IPv4_Unaffected pins down that IPv4 output is
// identical to msg.Marshal(nil) both before and after the fix: the x/net
// pseudo-header branch is gated on proto == ProtocolIPv6ICMP, so IPv4
// was never affected by the bug, and the fix must not change its output.
func TestMarshalProbe_IPv4_Unaffected(t *testing.T) {
	sizes := []int{0, 20, 27, 28, 56, 1399, 1400, 1401, 1500}

	for _, size := range sizes {
		t.Run("size_"+strconv.Itoa(size), func(t *testing.T) {
			payload := buildTestPayload(size)
			msg := &icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{ID: 1, Seq: 1, Data: payload},
			}

			got, err := marshalProbe(msg)
			if err != nil {
				t.Fatalf("marshalProbe returned error: %v", err)
			}

			want, err := msg.Marshal(nil)
			if err != nil {
				t.Fatalf("msg.Marshal(nil) returned error: %v", err)
			}

			if !bytes.Equal(got, want) {
				t.Fatalf("IPv4 marshalProbe output diverges from Marshal(nil) at size=%d\n got=% x\nwant=% x", size, got, want)
			}

			parsed, err := icmp.ParseMessage(icmpv4Proto, got)
			if err != nil {
				t.Fatalf("icmp.ParseMessage failed: %v", err)
			}
			echo, ok := parsed.Body.(*icmp.Echo)
			if !ok {
				t.Fatalf("parsed body is %T, want *icmp.Echo", parsed.Body)
			}
			if !bytes.Equal(echo.Data, payload) {
				t.Fatalf("IPv4 payload corrupted for size=%d\n got=% x\nwant=% x", size, echo.Data, payload)
			}
		})
	}
}
