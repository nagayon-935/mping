package pinger

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// hopErrorMsg builds an ICMP Time Exceeded / Destination Unreachable message
// (v4 or v6, chosen by typ) wrapping the echo request with the given id and
// seq — mirrors what a real router returns for a TTL-limited probe.
func hopErrorMsg(t *testing.T, typ icmp.Type, id, seq int) []byte {
	t.Helper()
	data := ipPacketWithEcho(typ.Protocol() == 58, id, seq)
	var body icmp.MessageBody = &icmp.DstUnreach{Data: data}
	if typ == ipv4.ICMPTypeTimeExceeded || typ == ipv6.ICMPTypeTimeExceeded {
		body = &icmp.TimeExceeded{Data: data}
	}
	return mustMarshal(t, &icmp.Message{Type: typ, Body: body})
}

func echoReplyMsg(t *testing.T, typ icmp.Type, id, seq int) []byte {
	t.Helper()
	return mustMarshal(t, &icmp.Message{Type: typ, Body: &icmp.Echo{ID: id, Seq: seq}})
}

func parseHopMsg(t *testing.T, proto int, raw []byte) *icmp.Message {
	t.Helper()
	msg, err := icmp.ParseMessage(proto, raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return msg
}

// newHopPinger returns a Pinger whose resolver maps every host to ip.
func newHopPinger(ip string) *Pinger {
	return NewPingerWithOptions(nil, Options{ResolveIPAddr: resolveTo(ip)})
}

func TestRegisterUnregisterTraceChan(t *testing.T) {
	// A zero-value Pinger has no map yet; Register must create it.
	p := &Pinger{}
	p.RegisterTraceChan(123)
	if len(p.traceChans) != 1 {
		t.Fatalf("want 1 traceChan, got %d", len(p.traceChans))
	}
	p.UnregisterTraceChan(123)
	if len(p.traceChans) != 0 {
		t.Fatalf("want 0 traceChans after unregister, got %d", len(p.traceChans))
	}
}

func TestNextTraceID(t *testing.T) {
	t.Run("legacy IDs are unique", func(t *testing.T) {
		p := &Pinger{baseID: 0}
		seen := make(map[int]bool)
		for i := range 100 {
			id := p.NextTraceID()
			if seen[id] {
				t.Errorf("duplicate traceID %d at iteration %d", id, i)
			}
			seen[id] = true
		}
	})

	t.Run("allocator IDs live in the route-probe range and can run out", func(t *testing.T) {
		p := NewPingerWithOptions(nil, Options{IDs: &IDAllocator{}})
		if id := p.NextTraceID(); id < 32768 {
			t.Fatalf("traceID %d overlaps the echo ID namespace", id)
		}
		full := &IDAllocator{traces: map[int]bool{}}
		for id := 32768; id < 65536; id++ {
			full.traces[id] = true
		}
		p.ids = full
		if id := p.NextTraceID(); id != -1 {
			t.Fatalf("NextTraceID = %d, want -1 when every route-probe ID is in use", id)
		}
	})
}

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

func TestAcceptHopPacket(t *testing.T) {
	const traceID, ttl = 0x1234, 5
	src := &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}

	tests := []struct {
		name      string
		raw       []byte
		id, ttl   int
		wantMatch bool
		wantDest  bool
	}{
		{"time exceeded from an intermediate hop", hopErrorMsg(t, ipv4.ICMPTypeTimeExceeded, traceID, ttl), traceID, ttl, true, false},
		{"echo reply means the destination answered", echoReplyMsg(t, ipv4.ICMPTypeEchoReply, traceID, ttl), traceID, ttl, true, true},
		{"destination unreachable also ends the trace", hopErrorMsg(t, ipv4.ICMPTypeDestinationUnreachable, traceID, ttl), traceID, ttl, true, true},
		{"reply for another trace ID", echoReplyMsg(t, ipv4.ICMPTypeEchoReply, 0x1111, ttl), traceID, ttl, false, false},
		{"reply for another hop", echoReplyMsg(t, ipv4.ICMPTypeEchoReply, traceID, ttl+1), traceID, ttl, false, false},
		{"time exceeded for another hop", hopErrorMsg(t, ipv4.ICMPTypeTimeExceeded, traceID, ttl+1), traceID, ttl, false, false},
		{"an echo request is not a hop reply", echoBytes(t, false, traceID, ttl), traceID, ttl, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reply, matched := acceptHopPacket(parseHopMsg(t, 1, tt.raw), src, tt.id, tt.ttl)
			if matched != tt.wantMatch {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatch)
			}
			if !matched {
				return
			}
			if !reply.Responded || reply.ReachedDest != tt.wantDest || reply.SrcIP != "10.0.0.1" {
				t.Fatalf("reply = %+v, want Responded, ReachedDest=%v, SrcIP=10.0.0.1", reply, tt.wantDest)
			}
		})
	}
}

func TestProbeHop_ViaTraceChan_TimeExceeded(t *testing.T) {
	p := newHopPinger("8.8.8.8")
	p.connV4 = &fakePacketConn{} // a running pinger: replies arrive through the shared receiver
	traceID := p.NextTraceID()
	// The shared receiver dispatches the router's reply as soon as the probe is on the wire.
	reply := parseHopMsg(t, 1, hopErrorMsg(t, ipv4.ICMPTypeTimeExceeded, traceID, 3))
	conn := &fakeHopConn{onWrite: func([]byte, net.Addr) error {
		p.broadcastTrace(reply, &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)})
		return nil
	}}
	sock := &HopSocket{sendV4: conn, isV4: true}
	defer sock.Close()

	got, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 3, traceID, 200*time.Millisecond)

	if err != nil {
		t.Fatalf("ProbeHop: %v", err)
	}
	if !got.Responded || got.ReachedDest || got.SrcIP != "10.0.0.1" {
		t.Fatalf("reply = %+v, want a non-final response from 10.0.0.1", got)
	}
}

func TestProbeHop_NoMatchingReplyTimesOut(t *testing.T) {
	t.Run("v4 socket", func(t *testing.T) {
		p := newHopPinger("8.8.8.8")
		sock := &HopSocket{sendV4: &fakeHopConn{}, isV4: true}
		reply, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 1, p.NextTraceID(), 30*time.Millisecond)
		if err != nil || reply.Responded {
			t.Fatalf("ProbeHop = (%+v, %v), want Responded=false and no error", reply, err)
		}
	})

	t.Run("v6 socket", func(t *testing.T) {
		p := newHopPinger("2001:4860:4860::8888")
		sock := &HopSocket{sendV6: &fakeHopConnV6{}}
		reply, err := p.ProbeHop(context.Background(), sock, "2001:4860:4860::8888", 1, p.NextTraceID(), 30*time.Millisecond)
		if err != nil || reply.Responded {
			t.Fatalf("ProbeHop = (%+v, %v), want Responded=false and no error", reply, err)
		}
	})

	t.Run("shared receiver delivers only unrelated replies", func(t *testing.T) {
		p := newHopPinger("8.8.8.8")
		p.connV4 = &fakePacketConn{}
		traceID := p.NextTraceID()
		// Right trace, wrong hop: acceptHopPacket must skip it and keep waiting.
		other := parseHopMsg(t, 1, hopErrorMsg(t, ipv4.ICMPTypeTimeExceeded, traceID, 9))
		sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{onWrite: func([]byte, net.Addr) error {
			p.broadcastTrace(other, &net.IPAddr{IP: net.IPv4(10, 0, 0, 9)})
			return nil
		}}}
		reply, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 3, traceID, 30*time.Millisecond)
		if err != nil || reply.Responded {
			t.Fatalf("ProbeHop = (%+v, %v), want Responded=false and no error", reply, err)
		}
	})
}

func TestProbeHop_Errors(t *testing.T) {
	t.Run("resolve failure", func(t *testing.T) {
		p := NewPingerWithOptions(nil, Options{
			ResolveIPAddr: func(string, string) (*net.IPAddr, error) { return nil, errors.New("resolve failed") },
		})
		sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{}}
		if _, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 1, 0x1234, time.Second); err == nil {
			t.Fatal("expected error when resolve fails")
		}
	})

	t.Run("route-probe IDs exhausted", func(t *testing.T) {
		p := newHopPinger("8.8.8.8")
		if _, err := p.ProbeHop(context.Background(), &HopSocket{isV4: true, sendV4: &fakeHopConn{}}, "8.8.8.8", 1, -1, time.Second); err == nil {
			t.Fatal("expected error for a negative trace ID")
		}
	})

	t.Run("trace ID is released after the probe", func(t *testing.T) {
		ids := &IDAllocator{}
		p := NewPingerWithOptions(nil, Options{IDs: ids, ResolveIPAddr: resolveTo("8.8.8.8")})
		traceID := p.NextTraceID()
		sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{}}
		if _, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 1, traceID, 10*time.Millisecond); err != nil {
			t.Fatalf("ProbeHop: %v", err)
		}
		if len(ids.traces) != 0 {
			t.Fatalf("trace ID still held after ProbeHop: %v", ids.traces)
		}
	})

	t.Run("context cancelled while waiting on the shared receiver", func(t *testing.T) {
		p := newHopPinger("8.8.8.8")
		p.connV4 = &fakePacketConn{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Cancel the moment the probe is on the wire, with a long timeout still pending.
		sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{onWrite: func([]byte, net.Addr) error {
			cancel()
			return nil
		}}}
		if _, err := p.ProbeHop(ctx, sock, "8.8.8.8", 1, p.NextTraceID(), 5*time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

// TestProbeHop_ReadsReplyFromOwnSocket covers the standalone path used when no
// Pinger receiver is running: the reply is read straight off the send socket.
func TestProbeHop_ReadsReplyFromOwnSocket(t *testing.T) {
	const traceID, ttl = 0x2345, 4
	hopAddr := &net.IPAddr{IP: net.ParseIP("203.0.113.7")}
	hopAddr6 := &net.IPAddr{IP: net.ParseIP("2001:db8::7")}

	tests := []struct {
		name     string
		v6       bool
		queue    []readResult
		wantDest bool
	}{
		{"time exceeded", false, []readResult{
			{data: hopErrorMsg(t, ipv4.ICMPTypeTimeExceeded, traceID, ttl), addr: hopAddr},
		}, false},
		{"unparseable and unrelated packets are skipped", false, []readResult{
			{data: []byte{0x01}, addr: hopAddr},                                              // too short for ICMP
			{data: hopErrorMsg(t, ipv4.ICMPTypeTimeExceeded, traceID, ttl+1), addr: hopAddr}, // another hop
			{data: echoReplyMsg(t, ipv4.ICMPTypeEchoReply, traceID, ttl), addr: hopAddr},
		}, true},
		{"v6 time exceeded", true, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sock *HopSocket
			dst := "203.0.113.7"
			if tt.v6 {
				dst = "2001:db8::7"
				sock = &HopSocket{sendV6: &fakeHopConnV6{readQueue: []readResultV6{
					{data: hopErrorMsg(t, ipv6.ICMPTypeTimeExceeded, traceID, ttl), addr: hopAddr6},
				}}}
			} else {
				sock = &HopSocket{isV4: true, sendV4: &fakeHopConn{readQueue: tt.queue}}
			}
			p := newHopPinger(dst)

			reply, err := p.ProbeHop(context.Background(), sock, dst, ttl, traceID, time.Second)

			if err != nil {
				t.Fatalf("ProbeHop: %v", err)
			}
			wantSrc := dst
			if !reply.Responded || reply.ReachedDest != tt.wantDest || reply.SrcIP != wantSrc || reply.RTT < 0 {
				t.Fatalf("reply = %+v, want Responded from %s with ReachedDest=%v and a non-negative RTT", reply, wantSrc, tt.wantDest)
			}
		})
	}
}

func TestProbeHop_OwnSocketFailures(t *testing.T) {
	deadlineErr := errors.New("socket closed")

	t.Run("v4 read deadline cannot be set", func(t *testing.T) {
		p := newHopPinger("8.8.8.8")
		sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{deadlineErr: deadlineErr}}
		if _, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 1, p.NextTraceID(), time.Second); !errors.Is(err, deadlineErr) {
			t.Fatalf("err = %v, want it to wrap %v", err, deadlineErr)
		}
	})

	t.Run("v6 read deadline cannot be set", func(t *testing.T) {
		p := newHopPinger("2001:db8::8")
		sock := &HopSocket{sendV6: &fakeHopConnV6{deadlineErr: deadlineErr}}
		if _, err := p.ProbeHop(context.Background(), sock, "2001:db8::8", 1, p.NextTraceID(), time.Second); !errors.Is(err, deadlineErr) {
			t.Fatalf("err = %v, want it to wrap %v", err, deadlineErr)
		}
	})

	t.Run("cancelled while reading", func(t *testing.T) {
		p := newHopPinger("8.8.8.8")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// An unrelated packet keeps the read loop going; cancelling from the
		// write hook means the loop's next ctx check observes it.
		sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{
			readQueue: []readResult{{data: hopErrorMsg(t, ipv4.ICMPTypeTimeExceeded, 1, 99)}},
			onWrite:   func([]byte, net.Addr) error { cancel(); return nil },
		}}
		if _, err := p.ProbeHop(ctx, sock, "8.8.8.8", 1, p.NextTraceID(), time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

// TestProbeHopAddr_ConcurrentIPv4SendsDoNotRaceOnTTL reproduces the MTR
// discover()/probe() calling pattern: many goroutines call probeHopAddr
// concurrently with different TTLs on one shared HopSocket. IPv4's TTL is
// connection-wide state, so each goroutine's WriteTo must observe the TTL it
// just set, not a value clobbered by another goroutine's concurrent SetTTL —
// otherwise probes for different hops are sent (and therefore answered) as if
// they were the same hop, which is exactly the "every hop shows the same
// nearby IP, route 'completes' after 2-3 hops" symptom reported against MTR.
// The artificial delay inside SetTTL widens the window so the race is
// reliably observable instead of depending on scheduler luck.
func TestProbeHopAddr_ConcurrentIPv4SendsDoNotRaceOnTTL(t *testing.T) {
	conn := &fakeHopConn{ttlDelay: 2 * time.Millisecond}
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
	got := append([]int{}, conn.sentTTLs...)
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

// TestHopReplyDuringWriteIsDelivered: the shared receiver can dispatch a
// reply before WriteTo even returns, so the trace channel must be registered
// first or a fast response would be lost.
func TestHopReplyDuringWriteIsDelivered(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		p := NewPinger(nil)
		dst := &net.IPAddr{IP: net.ParseIP("127.0.0.1")}
		sock := &HopSocket{isV4: !v6}
		// The fast "response": the probe just written, echoed back as a reply.
		reply := func(proto int, replyType icmp.Type) func([]byte, net.Addr) error {
			return func(b []byte, dst net.Addr) error {
				msg, _ := icmp.ParseMessage(proto, b)
				msg.Type = replyType
				p.broadcastTrace(msg, dst)
				return nil
			}
		}
		if v6 {
			p.connV6 = &fakePacketConnV6{}
			sock.sendV6 = &fakeHopConnV6{onWrite: reply(58, ipv6.ICMPTypeEchoReply)}
			dst.IP = net.ParseIP("::1")
		} else {
			p.connV4 = &fakePacketConn{}
			sock.sendV4 = &fakeHopConn{onWrite: reply(1, ipv4.ICMPTypeEchoReply)}
		}

		got, err := p.probeHopAddr(context.Background(), sock, dst, 1, 123, 50*time.Millisecond)

		if err != nil || !got.Responded || !got.ReachedDest {
			t.Fatalf("v6=%v fast response lost: %+v %v", v6, got, err)
		}
		if len(p.traceChans) != 0 {
			t.Fatal("trace channel leaked")
		}
	}
}

func TestFailedHopSendUnregistersChannel(t *testing.T) {
	p := NewPinger(nil)
	p.connV4 = &fakePacketConn{}
	sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{onWrite: func([]byte, net.Addr) error {
		return errors.New("send failed")
	}}}
	if _, err := p.probeHopAddr(context.Background(), sock, &net.IPAddr{IP: net.ParseIP("127.0.0.1")}, 1, 10, time.Second); err == nil {
		t.Fatal("expected send error")
	}
	if len(p.traceChans) != 0 {
		t.Fatal("trace channel leaked after failed send")
	}
}

// TestHopOperationsCancelDNS checks every hop-based operation abandons a
// pending DNS lookup as soon as its context is cancelled, for both the
// context-aware and the legacy context-free resolver hooks.
func TestHopOperationsCancelDNS(t *testing.T) {
	for _, operation := range []string{"traceroute", "open", "probe", "pmtu"} {
		for _, legacy := range []bool{false, true} {
			t.Run(operation+map[bool]string{true: "/legacy", false: "/context"}[legacy], func(t *testing.T) {
				entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
				opts := Options{}
				if legacy {
					opts.ResolveIPAddr = func(string, string) (*net.IPAddr, error) {
						close(entered)
						defer close(exited)
						<-release
						return nil, context.Canceled
					}
				} else {
					opts.ResolveIPAddrContext = func(ctx context.Context, _, _ string) (*net.IPAddr, error) {
						close(entered)
						defer close(exited)
						<-ctx.Done()
						return nil, ctx.Err()
					}
				}
				p := NewPingerWithOptions(nil, opts)
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(func() { cancel(); close(release); <-exited })
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "traceroute":
						_, err = p.TraceRoute(ctx, "example.invalid", 1, time.Second)
					case "open":
						_, err = p.OpenHopSocketContext(ctx, "example.invalid")
					case "probe":
						_, err = p.ProbeHop(ctx, nil, "example.invalid", 1, 1, time.Second)
					case "pmtu":
						_, _, err = p.DiscoverMaxPayload(ctx, "example.invalid", 1500, 56, nil)
					}
					done <- err
				}()
				<-entered
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancellation: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("DNS ignored operation cancellation")
				}
			})
		}
	}
}

func TestOpenHopSocket_Failures(t *testing.T) {
	tests := []struct {
		name    string
		resolve func(network, address string) (*net.IPAddr, error)
		listen  func(network, address string) (net.PacketConn, error)
		wantMsg string
	}{
		{"resolve failure", func(string, string) (*net.IPAddr, error) { return nil, errors.New("resolve failed") }, nil, "resolve"},
		{"v4 listen failure", resolveTo("8.8.8.8"), func(string, string) (net.PacketConn, error) { return nil, errors.New("listen: permission denied") }, "MTR send socket (v4)"},
		{"v6 listen failure", resolveTo("2001:4860:4860::8888"), func(string, string) (net.PacketConn, error) { return nil, errors.New("listen: permission denied") }, "MTR send socket (v6)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{ResolveIPAddr: tt.resolve, ListenPacket: tt.listen})
			_, err := p.OpenHopSocket("example.com")
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

func TestHopSocket_Close(t *testing.T) {
	v4, v6 := &fakeHopConn{}, &fakeHopConnV6{}

	(&HopSocket{isV4: true, sendV4: v4}).Close()
	if !v4.closed || v6.closed {
		t.Fatalf("closing a v4 socket: v4 closed=%v v6 closed=%v, want only v4", v4.closed, v6.closed)
	}
	(&HopSocket{sendV6: v6}).Close()
	if !v6.closed {
		t.Fatal("closing a v6 socket did not close its connection")
	}

	// A socket whose connection never opened must close cleanly.
	(&HopSocket{isV4: true}).Close()
	(&HopSocket{}).Close()
}

// TestReceiveViaTraceChan_ClosedChannelIsATimeout pins the guard for a closed
// trace channel: nothing closes one today (see UnregisterTraceChan), but if
// something did, the probe must report no response instead of handing a zero
// traceMsg to accept or spinning on the closed channel.
func TestReceiveViaTraceChan_ClosedChannelIsATimeout(t *testing.T) {
	ch := make(chan traceMsg)
	close(ch)
	accept := func(*icmp.Message, net.Addr) (HopReply, bool) {
		t.Fatal("accept called for a closed channel")
		return HopReply{}, false
	}
	got, err := receiveViaTraceChan(context.Background(), ch, accept, time.Now(), time.Hour)
	if err != nil || got.Responded {
		t.Fatalf("got %+v, %v; want a non-responded hop and no error", got, err)
	}
}
