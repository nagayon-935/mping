package pinger

import (
	"context"
	"net"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// fakeHopConn implements hopSendConnV4 for tests.
type fakeHopConn struct{}

func (f *fakeHopConn) SetTTL(ttl int) error { return nil }
func (f *fakeHopConn) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) {
	return len(b), nil
}
func (f *fakeHopConn) SetReadDeadline(t time.Time) error { return nil }
func (f *fakeHopConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	return 0, nil, nil, timeoutOpError()
}
func (f *fakeHopConn) Close() error { return nil }

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
	p := &Pinger{
		traceChans: make(map[int]chan traceMsg),
	}
	p.RegisterTraceChan(123)
	if len(p.traceChans) != 1 {
		t.Fatalf("want 1 traceChan, got %d", len(p.traceChans))
	}
	p.UnregisterTraceChan(123)
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
		lookupTXT:  func(name string) ([]string, error) { return nil, nil },
		traceChans: make(map[int]chan traceMsg),
	}
	p.connV4 = &fakePacketConnV4{}

	sock := &HopSocket{
		sendV4: &fakeHopConn{},
		isV4:   true,
		pinger: p,
	}
	defer sock.Close()

	traceID := p.NextTraceID()

	// Inject a matching Time Exceeded into the traceChan after it is registered
	raw := buildTimeExceededMsg(traceID, 3)
	msg, _ := icmp.ParseMessage(1, raw)
	go func() {
		var ch chan traceMsg
		for i := 0; i < 100; i++ {
			p.traceChansMu.RLock()
			ch = p.traceChans[traceID]
			p.traceChansMu.RUnlock()
			if ch != nil {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		if ch != nil {
			ch <- traceMsg{parsed: msg, src: &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}}
		}
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

	sock := &HopSocket{
		sendV4: &fakeHopConn{},
		isV4:   true,
		pinger: p,
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
		lookupTXT:  func(name string) ([]string, error) { return nil, nil },
		traceChans: make(map[int]chan traceMsg),
	}
	p.connV4 = &fakePacketConnV4{}

	sock := &HopSocket{
		sendV4: &fakeHopConn{},
		isV4:   true,
		pinger: p,
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

// racyTTLConn simulates a real IPv4 raw socket: SetTTL mutates one
// connection-wide TTL value (there is no per-packet TTL ancillary data on
// IPv4, unlike IPv6's HopLimit control message — see the "TTL ... receiving
// only" comment on ipv4.ControlMessage), and WriteTo records whatever TTL is
// current at the moment it is called. The artificial delay inside SetTTL
// widens the window between "set" and "write" so a race between concurrent
// callers sharing one HopSocket becomes reliably observable instead of
// depending on scheduler luck.
type racyTTLConn struct {
	mu      sync.Mutex
	current int
	seen    []int
}

func (f *racyTTLConn) SetTTL(ttl int) error {
	f.mu.Lock()
	f.current = ttl
	f.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	return nil
}

func (f *racyTTLConn) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) {
	f.mu.Lock()
	f.seen = append(f.seen, f.current)
	f.mu.Unlock()
	return len(b), nil
}

func (f *racyTTLConn) SetReadDeadline(t time.Time) error { return nil }
func (f *racyTTLConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	return 0, nil, nil, timeoutOpError()
}
func (f *racyTTLConn) Close() error { return nil }

// TestProbeHopAddr_ConcurrentIPv4SendsDoNotRaceOnTTL reproduces the MTR
// discover()/probe() calling pattern: many goroutines call probeHopAddr
// concurrently with different TTLs on one shared HopSocket. Each goroutine's
// WriteTo must observe the TTL it just set, not a value clobbered by another
// goroutine's concurrent SetTTL — otherwise probes for different hops are
// sent (and therefore answered) as if they were the same hop, which is
// exactly the "every hop shows the same nearby IP, route 'completes' after
// 2-3 hops" symptom reported against MTR.
func TestProbeHopAddr_ConcurrentIPv4SendsDoNotRaceOnTTL(t *testing.T) {
	conn := &racyTTLConn{}
	sock := &HopSocket{isV4: true, sendV4: conn}
	p := &Pinger{baseID: 0x1000}
	dstAddr := &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}

	const n = 10
	var wg sync.WaitGroup
	for ttl := 1; ttl <= n; ttl++ {
		wg.Add(1)
		go func(ttl int) {
			defer wg.Done()
			_, _ = p.probeHopAddr(context.Background(), sock, dstAddr, ttl, p.NextTraceID(), 20*time.Millisecond)
		}(ttl)
	}
	wg.Wait()

	conn.mu.Lock()
	got := append([]int{}, conn.seen...)
	conn.mu.Unlock()

	sort.Ints(got)
	want := make([]int, n)
	for i := range want {
		want[i] = i + 1
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TTL race: WriteTo saw %v, want each hop's own TTL exactly once (%v) — "+
			"SetTTL+WriteTo must be atomic per call when HopSocket is shared across goroutines", got, want)
	}
}
