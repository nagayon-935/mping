package pinger

import (
	"sync"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// benchMarshalBufPool reproduces the pooled-buffer strategy pinger.go used
// before this fix, kept here only so we can measure whether it was ever
// worth the complexity. See marshalProbe's doc comment in pinger.go for why
// it was removed from production code: for ICMPv6 it silently corrupted
// probe payloads, and for ICMPv4 icmp.Message.Marshal never actually reused
// the buffer in the first place (it allocates a fresh 4-byte header slice
// unconditionally; the psh argument only feeds into IPv6's pseudo-header
// path), so the pool bought nothing there either.
var benchMarshalBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 1500)
		return &b
	},
}

func marshalWithPooledBuffer(msg *icmp.Message) ([]byte, error) {
	bufPtr := benchMarshalBufPool.Get().(*[]byte)
	defer benchMarshalBufPool.Put(bufPtr)
	buf := *bufPtr
	return msg.Marshal(buf[:0])
}

func BenchmarkMarshalProbe_IPv4_Nil(b *testing.B) {
	msg := &icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: 1, Seq: 1, Data: buildTestPayload(56)},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := marshalProbe(msg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalProbe_IPv4_PooledBuffer(b *testing.B) {
	msg := &icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: 1, Seq: 1, Data: buildTestPayload(56)},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := marshalWithPooledBuffer(msg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalProbe_IPv6_Nil(b *testing.B) {
	msg := &icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{ID: 1, Seq: 1, Data: buildTestPayload(56)},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := marshalProbe(msg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalProbe_IPv6_PooledBuffer(b *testing.B) {
	msg := &icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{ID: 1, Seq: 1, Data: buildTestPayload(56)},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := marshalWithPooledBuffer(msg); err != nil {
			b.Fatal(err)
		}
	}
}
