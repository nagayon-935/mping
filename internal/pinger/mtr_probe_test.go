package pinger

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// fakeHopConn implements hopSendConnV4 for tests.
type fakeHopConn struct{}

func (f *fakeHopConn) SetTTL(ttl int) error                                                    { return nil }
func (f *fakeHopConn) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error)   { return len(b), nil }
func (f *fakeHopConn) SetReadDeadline(t time.Time) error                                       { return nil }
func (f *fakeHopConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error)         { return 0, nil, nil, timeoutOpError() }
func (f *fakeHopConn) Close() error                                                            { return nil }

// buildTimeExceededMsg builds an ICMPv4 Time Exceeded message wrapping an Echo
// with the given id and seq — mirrors what a real router returns.
func buildTimeExceededMsg(id, seq int) []byte {
	inner, _ := (&icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{ID: id, Seq: seq, Data: make([]byte, 8)},
	}).Marshal(nil)

	// Time Exceeded payload: 4-byte unused + original IP header (20 bytes) + first 8 bytes of ICMP
	unused := make([]byte, 4)
	// Minimal fake IPv4 header (20 bytes)
	ipHdr := make([]byte, 20)
	ipHdr[0] = 0x45 // version=4, IHL=5
	ipHdr[9] = 1    // protocol ICMP
	payload := append(unused, append(ipHdr, inner[:8]...)...)

	msg, _ := (&icmp.Message{
		Type: ipv4.ICMPTypeTimeExceeded, Code: 0,
		Body: &icmp.RawBody{Data: payload},
	}).Marshal(nil)
	return msg
}

func buildEchoReplyMsg(id, seq int) []byte {
	msg, _ := (&icmp.Message{
		Type: ipv4.ICMPTypeEchoReply, Code: 0,
		Body: &icmp.Echo{ID: id, Seq: seq},
	}).Marshal(nil)
	return msg
}

func TestRegisterUnregisterTraceChan(t *testing.T) {
	p := &Pinger{}
	ch := p.RegisterTraceChan()
	if len(p.traceChans) != 1 {
		t.Fatalf("want 1 traceChan, got %d", len(p.traceChans))
	}
	p.UnregisterTraceChan(ch)
	if len(p.traceChans) != 0 {
		t.Fatalf("want 0 traceChans after unregister, got %d", len(p.traceChans))
	}
}

func TestAcceptHopPacket_TimeExceeded(t *testing.T) {
	traceID := 0x1234
	ttl := 3

	raw := buildTimeExceededMsg(traceID, ttl)
	msg, err := icmp.ParseMessage(1, raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	src := &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}
	reply, matched := acceptHopPacket(msg, src, traceID, ttl)
	if !matched {
		t.Fatal("expected match for Time Exceeded")
	}
	if reply.ReachedDest {
		t.Error("Time Exceeded should not set ReachedDest")
	}
	if !reply.Responded {
		t.Error("expected Responded=true")
	}
	if reply.SrcIP != "10.0.0.1" {
		t.Errorf("SrcIP: want 10.0.0.1, got %q", reply.SrcIP)
	}
}

func TestAcceptHopPacket_EchoReply(t *testing.T) {
	traceID := 0xABCD
	ttl := 5

	raw := buildEchoReplyMsg(traceID, ttl)
	msg, err := icmp.ParseMessage(1, raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	src := &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}
	reply, matched := acceptHopPacket(msg, src, traceID, ttl)
	if !matched {
		t.Fatal("expected match for EchoReply")
	}
	if !reply.ReachedDest {
		t.Error("EchoReply should set ReachedDest")
	}
}

func TestAcceptHopPacket_WrongID(t *testing.T) {
	raw := buildEchoReplyMsg(0x1111, 1)
	msg, _ := icmp.ParseMessage(1, raw)
	_, matched := acceptHopPacket(msg, nil, 0x2222, 1)
	if matched {
		t.Error("should not match wrong traceID")
	}
}

func TestAcceptHopPacket_WrongSeq(t *testing.T) {
	raw := buildEchoReplyMsg(0x1111, 1)
	msg, _ := icmp.ParseMessage(1, raw)
	_, matched := acceptHopPacket(msg, nil, 0x1111, 2)
	if matched {
		t.Error("should not match wrong seq/ttl")
	}
}

func TestProbeHop_ViaTraceChan_TimeExceeded(t *testing.T) {
	p := &Pinger{
		baseID: 0x1000,
		resolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}, nil
		},
		lookupTXT: func(name string) ([]string, error) { return nil, nil },
	}

	// Build HopSocket directly with a fake send conn and a pre-registered traceChan.
	traceCh := p.RegisterTraceChan()
	sock := &HopSocket{
		sendV4:  &fakeHopConn{},
		traceCh: traceCh,
		isV4:    true,
		pinger:  p,
	}
	defer sock.Close()

	traceID := p.NextTraceID()

	// Inject a matching Time Exceeded into the traceChan
	raw := buildTimeExceededMsg(traceID, 3)
	msg, _ := icmp.ParseMessage(1, raw)
	go func() {
		time.Sleep(5 * time.Millisecond)
		sock.traceCh <- traceMsg{parsed: msg, src: &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}}
	}()

	ctx := context.Background()
	reply, err := p.ProbeHop(ctx, sock, "8.8.8.8", 3, traceID, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("ProbeHop: %v", err)
	}
	if !reply.Responded {
		t.Error("expected Responded=true")
	}
	if reply.ReachedDest {
		t.Error("Time Exceeded should not set ReachedDest")
	}
	if reply.SrcIP != "10.0.0.1" {
		t.Errorf("SrcIP: want 10.0.0.1, got %q", reply.SrcIP)
	}
}

func TestProbeHop_Timeout(t *testing.T) {
	p := &Pinger{
		baseID: 0x1000,
		resolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}, nil
		},
		lookupTXT: func(name string) ([]string, error) { return nil, nil },
	}

	traceCh := p.RegisterTraceChan()
	sock := &HopSocket{
		sendV4:  &fakeHopConn{},
		traceCh: traceCh,
		isV4:    true,
		pinger:  p,
	}
	defer sock.Close()

	ctx := context.Background()
	reply, err := p.ProbeHop(ctx, sock, "8.8.8.8", 1, p.NextTraceID(), 30*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply.Responded {
		t.Error("expected Responded=false on timeout")
	}
}

func TestProbeHop_CtxCancel(t *testing.T) {
	p := &Pinger{
		baseID: 0x1000,
		resolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}, nil
		},
		lookupTXT: func(name string) ([]string, error) { return nil, nil },
	}

	traceCh := p.RegisterTraceChan()
	sock := &HopSocket{
		sendV4:  &fakeHopConn{},
		traceCh: traceCh,
		isV4:    true,
		pinger:  p,
	}
	defer sock.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := p.ProbeHop(ctx, sock, "8.8.8.8", 1, p.NextTraceID(), 5*time.Second)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestNextTraceID_Unique(t *testing.T) {
	p := &Pinger{baseID: 0}
	ids := make(map[int]bool)
	for i := 0; i < 100; i++ {
		id := p.NextTraceID()
		if ids[id] {
			t.Errorf("duplicate traceID %d at iteration %d", id, i)
		}
		ids[id] = true
	}
}
