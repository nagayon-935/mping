package pinger

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/ipv6"
)

// fakeHopConnV6 implements hopSendConnV6 for unit tests.
type fakeHopConnV6 struct{}

func (f *fakeHopConnV6) SetHopLimit(ttl int) error { return nil }
func (f *fakeHopConnV6) WriteTo(b []byte, cm *ipv6.ControlMessage, dst net.Addr) (int, error) {
	return len(b), nil
}
func (f *fakeHopConnV6) SetReadDeadline(t time.Time) error { return nil }
func (f *fakeHopConnV6) ReadFrom(b []byte) (int, *ipv6.ControlMessage, net.Addr, error) {
	return 0, nil, nil, timeoutOpError()
}
func (f *fakeHopConnV6) Close() error { return nil }

// TestNewPingerWithOptions_WithResolver covers the opts.Resolver != nil path
// for both the resolve and lookupTXT closures.
func TestNewPingerWithOptions_WithResolver(t *testing.T) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return nil, fmt.Errorf("mock: no dial")
		},
	}
	p := NewPingerWithOptions(nil, Options{Resolver: r})
	if p == nil {
		t.Fatal("expected non-nil Pinger")
	}
	_, _ = p.resolveIPAddr("ip4", "nonexistent.invalid.")
	_, _ = p.lookupTXT("nonexistent.invalid.")
}

// TestTraceRoute_ListenPacketFailureV4 covers the v4 send-socket open error path.
func TestTraceRoute_ListenPacketFailureV4(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("8.8.8.8")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, fmt.Errorf("listen: permission denied")
		},
	})
	_, err := p.TraceRoute("8.8.8.8", 5, time.Second)
	if err == nil {
		t.Fatal("expected error when listenPacket fails")
	}
	if !strings.Contains(err.Error(), "traceroute send socket") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestTraceRoute_ListenPacketFailureV6 covers the v6 send-socket open error path.
func TestTraceRoute_ListenPacketFailureV6(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("2001:4860:4860::8888")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, fmt.Errorf("listen: permission denied")
		},
	})
	_, err := p.TraceRoute("2001:4860:4860::8888", 5, time.Second)
	if err == nil {
		t.Fatal("expected error when listenPacket fails (v6)")
	}
	if !strings.Contains(err.Error(), "traceroute send socket") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestOpenHopSocket_ResolveFailure covers the resolve error path.
func TestOpenHopSocket_ResolveFailure(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return nil, fmt.Errorf("resolve failed")
		},
	})
	_, err := p.OpenHopSocket("8.8.8.8")
	if err == nil {
		t.Fatal("expected error when resolve fails")
	}
}

// TestOpenHopSocket_ListenPacketFailureV4 covers v4 socket open failure.
func TestOpenHopSocket_ListenPacketFailureV4(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("8.8.8.8")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, fmt.Errorf("listen: permission denied")
		},
	})
	_, err := p.OpenHopSocket("8.8.8.8")
	if err == nil {
		t.Fatal("expected error when listenPacket fails (v4)")
	}
	if !strings.Contains(err.Error(), "MTR send socket") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestOpenHopSocket_ListenPacketFailureV6 covers v6 socket open failure.
func TestOpenHopSocket_ListenPacketFailureV6(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("2001:4860:4860::8888")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, fmt.Errorf("listen: permission denied")
		},
	})
	_, err := p.OpenHopSocket("2001:4860:4860::8888")
	if err == nil {
		t.Fatal("expected error when listenPacket fails (v6)")
	}
	if !strings.Contains(err.Error(), "MTR send socket") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestHopSocket_CloseV6 covers the v6 branch of HopSocket.Close.
func TestHopSocket_CloseV6(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	sock := &HopSocket{
		isV4:   false,
		sendV6: &fakeHopConnV6{},
		pinger: p,
	}
	sock.Close()
}

// TestCanSendPayload_ListenPacketFailure covers the listenPacket error path.
func TestCanSendPayload_ListenPacketFailure(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, fmt.Errorf("listen: permission denied")
		},
	})
	ok, bottleneck, err := p.canSendPayload(&net.IPAddr{IP: net.ParseIP("8.8.8.8")}, 100)
	if err == nil {
		t.Fatal("expected error when listenPacket fails")
	}
	if ok {
		t.Error("expected ok=false on error")
	}
	if bottleneck != "" {
		t.Errorf("expected empty bottleneck, got %q", bottleneck)
	}
}

// TestCanSendPayload_NegativePayload covers the payloadLen < 0 normalization.
func TestCanSendPayload_NegativePayload(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, fmt.Errorf("listen: permission denied")
		},
	})
	_, _, err := p.canSendPayload(&net.IPAddr{IP: net.ParseIP("8.8.8.8")}, -1)
	// Error is expected (listenPacket fails); we just want the payloadLen < 0 branch covered.
	if err == nil {
		t.Fatal("expected error when listenPacket fails")
	}
}

// TestProbeHop_ResolveFailure covers the resolve error return in ProbeHop.
func TestProbeHop_ResolveFailure(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return nil, fmt.Errorf("resolve failed")
		},
	})
	sock := &HopSocket{isV4: true, sendV4: &fakeHopConn{}, pinger: p}
	_, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 1, 0x1234, time.Second)
	if err == nil {
		t.Fatal("expected error when resolve fails")
	}
}

// TestProbeHop_NoTraceCh_V4 covers ProbeHop without a traceChan (receiveViaSocket v4 path).
// fakeHopConn.ReadFrom returns a timeout, so ProbeHop returns Responded=false.
func TestProbeHop_NoTraceCh_V4(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("8.8.8.8")}, nil
		},
	})
	sock := &HopSocket{
		isV4:   true,
		sendV4: &fakeHopConn{},
		pinger: p,
		// traceCh intentionally nil to exercise receiveViaSocket
	}
	reply, err := p.ProbeHop(context.Background(), sock, "8.8.8.8", 1, 0x1234, 50*time.Millisecond)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if reply.Responded {
		t.Error("expected Responded=false on timeout")
	}
}

// TestProbeHop_NoTraceCh_V6 covers the v6 branch of ProbeHop and receiveViaSocket.
func TestProbeHop_NoTraceCh_V6(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("2001:4860:4860::8888")}, nil
		},
	})
	sock := &HopSocket{
		isV4:   false,
		sendV6: &fakeHopConnV6{},
		pinger: p,
	}
	reply, err := p.ProbeHop(context.Background(), sock, "2001:4860:4860::8888", 1, 0x1234, 50*time.Millisecond)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if reply.Responded {
		t.Error("expected Responded=false on timeout")
	}
}

// TestProbeHop_ContextCancelled covers the ctx.Done() path in receiveViaTraceChan.
func TestProbeHop_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("8.8.8.8")}, nil
		},
	})
	traceCh := make(chan traceMsg, 1)
	sock := &HopSocket{
		isV4:    true,
		sendV4:  &fakeHopConn{},
		traceCh: traceCh,
		pinger:  p,
	}
	_, err := p.ProbeHop(ctx, sock, "8.8.8.8", 1, 0x1234, time.Second)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}
