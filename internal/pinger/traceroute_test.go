package pinger

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestTraceRoute_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		resolve func(network, address string) (*net.IPAddr, error)
		dest    string
		maxHops int
	}{
		{"empty destination", resolveTo("1.1.1.1"), "", 30},
		{"non-positive maxHops", resolveTo("1.1.1.1"), "example.com", 0},
		{"resolve failure", func(string, string) (*net.IPAddr, error) { return nil, errors.New("resolve failed") }, "example.com", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{ResolveIPAddr: tt.resolve})
			if _, err := p.TraceRoute(context.Background(), tt.dest, tt.maxHops, time.Second); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestTraceRoute_ListenFailure(t *testing.T) {
	for _, dst := range []string{"1.1.1.1", "2001:db8::1"} {
		t.Run(dst, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{
				ResolveIPAddr: resolveTo(dst),
				ListenPacket: func(string, string) (net.PacketConn, error) {
					return nil, errors.New("listen: permission denied")
				},
			})
			_, err := p.TraceRoute(context.Background(), "example.com", 3, time.Second)
			if err == nil || !strings.Contains(err.Error(), "traceroute send socket") {
				t.Fatalf("err = %v, want the send-socket open failure", err)
			}
		})
	}
}

// TestTraceRoute_UnusableSendSocketYieldsStarHops covers TraceRoute's
// per-hop failure policy: when a hop's probe cannot be sent (the fake
// PacketConn is not a real syscall.Conn, so the ipv4/ipv6 wrapper built
// around it rejects every write) that hop is reported as "*" and the trace
// carries on to maxHops instead of aborting. Pinger receiver sockets being
// present or absent only changes whether replies would be read from the
// shared receiver or from the send socket.
func TestTraceRoute_UnusableSendSocketYieldsStarHops(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		source   string
		attachV4 bool
		attachV6 bool
		maxHops  int
	}{
		{name: "IPv4", dst: "1.1.1.1", maxHops: 3},
		{name: "IPv4 with source", dst: "1.1.1.1", source: "10.0.0.1", maxHops: 1},
		{name: "IPv4 with running pinger", dst: "1.1.1.1", attachV4: true, maxHops: 1},
		{name: "IPv6", dst: "2001:db8::1", maxHops: 1},
		{name: "IPv6 with source", dst: "2001:db8::1", source: "2001:db8::2", maxHops: 1},
		{name: "IPv6 with running pinger", dst: "2001:db8::1", attachV6: true, maxHops: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{
				ResolveIPAddr: resolveTo(tt.dst),
				ListenPacket:  listenWith(&fakeNetPacketConn{}),
			})
			p.Source = tt.source
			if tt.attachV4 {
				p.connV4 = &fakePacketConn{}
			}
			if tt.attachV6 {
				p.connV6 = &fakePacketConnV6{}
			}

			hops, err := p.TraceRoute(context.Background(), "example.com", tt.maxHops, 10*time.Millisecond)

			if err != nil {
				t.Fatalf("TraceRoute: %v", err)
			}
			if len(hops) != tt.maxHops {
				t.Fatalf("got %d hops %v, want %d", len(hops), hops, tt.maxHops)
			}
			for i, hop := range hops {
				if hop != "*" {
					t.Fatalf("hop %d = %q, want %q", i, hop, "*")
				}
			}
			if len(p.traceChans) != 0 {
				t.Fatalf("%d trace channels leaked", len(p.traceChans))
			}
		})
	}
}

func TestTraceRoute_ContextCancelledBeforeFirstHop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := NewPingerWithOptions(nil, Options{
		// Resolving succeeds but cancels the trace, as a reset would.
		ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
			cancel()
			return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
		},
		ListenPacket: listenWith(&fakeNetPacketConn{}),
	})

	hops, err := p.TraceRoute(ctx, "example.com", 5, time.Second)

	if !errors.Is(err, context.Canceled) || hops != nil {
		t.Fatalf("TraceRoute = (%v, %v), want (nil, context.Canceled)", hops, err)
	}
}

func TestTraceRoute_RouteProbeIDs(t *testing.T) {
	newPinger := func(ids *IDAllocator) *Pinger {
		return NewPingerWithOptions(nil, Options{
			IDs:           ids,
			ResolveIPAddr: resolveTo("1.1.1.1"),
			ListenPacket:  listenWith(&fakeNetPacketConn{}),
		})
	}

	t.Run("exhausted IDs fail the trace", func(t *testing.T) {
		full := &IDAllocator{traces: map[int]bool{}}
		for id := 32768; id < 65536; id++ {
			full.traces[id] = true
		}
		hops, err := newPinger(full).TraceRoute(context.Background(), "example.com", 3, 10*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "exhausted") || hops != nil {
			t.Fatalf("TraceRoute = (%v, %v), want an exhaustion error", hops, err)
		}
	})

	t.Run("the trace ID is released when the trace ends", func(t *testing.T) {
		ids := &IDAllocator{}
		if _, err := newPinger(ids).TraceRoute(context.Background(), "example.com", 2, 10*time.Millisecond); err != nil {
			t.Fatalf("TraceRoute: %v", err)
		}
		if len(ids.traces) != 0 {
			t.Fatalf("%d trace IDs still held after the trace", len(ids.traces))
		}
	})
}
