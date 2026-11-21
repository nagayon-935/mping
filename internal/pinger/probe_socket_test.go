package pinger

import (
	"errors"
	"net"
	"testing"
	"time"
)

// bindCall records a single invocation of bindToInterfaceFn, captured by the
// spies below.
type bindCall struct {
	ifaceName string
	isIPv6    bool
}

// spyBindToInterface installs a bindToInterfaceFn seam that records every
// call instead of touching real sockets, and restores the original on
// t.Cleanup. This lets tests verify Start()/OpenHopSocket() dispatch to the
// platform-specific bind implementation with the right arguments without
// requiring root privileges or real network interfaces.
func spyBindToInterface(t *testing.T) *[]bindCall {
	t.Helper()
	orig := bindToInterfaceFn
	calls := &[]bindCall{}
	bindToInterfaceFn = func(_ net.PacketConn, ifaceName string, isIPv6 bool) {
		*calls = append(*calls, bindCall{ifaceName: ifaceName, isIPv6: isIPv6})
	}
	t.Cleanup(func() { bindToInterfaceFn = orig })
	return calls
}

// TestStart_DualStack_DispatchesBindToInterfaceForBothFamilies verifies that
// when Interface is set and neither Source nor -4/-6 restricts the pinger to
// a single family, Start() asks the platform layer to bind both the v4 and
// v6 sockets to the requested interface.
func TestStart_DualStack_DispatchesBindToInterfaceForBothFamilies(t *testing.T) {
	calls := spyBindToInterface(t)

	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Interface = "eth0"
	if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Close()

	if len(*calls) != 2 {
		t.Fatalf("expected 2 bindToInterfaceFn calls (v4+v6), got %d: %+v", len(*calls), *calls)
	}
	sawV4, sawV6 := false, false
	for _, c := range *calls {
		if c.ifaceName != "eth0" {
			t.Errorf("expected ifaceName %q, got %q", "eth0", c.ifaceName)
		}
		if c.isIPv6 {
			sawV6 = true
		} else {
			sawV4 = true
		}
	}
	if !sawV4 || !sawV6 {
		t.Errorf("expected one v4 call and one v6 call, got %+v", *calls)
	}
}

// TestStart_IPv4Source_DispatchesBindToInterfaceOnce verifies that when
// Source pins the pinger to IPv4 only, Start() calls bindToInterfaceFn
// exactly once, for the v4 socket, with isIPv6=false.
func TestStart_IPv4Source_DispatchesBindToInterfaceOnce(t *testing.T) {
	calls := spyBindToInterface(t)

	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Source = "1.1.1.1"
	p.Interface = "eth0"
	if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Close()

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 bindToInterfaceFn call, got %d: %+v", len(*calls), *calls)
	}
	if (*calls)[0].ifaceName != "eth0" || (*calls)[0].isIPv6 {
		t.Errorf("unexpected call: %+v", (*calls)[0])
	}
}

// TestStart_IPv6Source_DispatchesBindToInterfaceOnce mirrors the IPv4 case
// above for an IPv6 Source.
func TestStart_IPv6Source_DispatchesBindToInterfaceOnce(t *testing.T) {
	calls := spyBindToInterface(t)

	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Source = "2001:db8::1"
	p.Interface = "eth0"
	if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Close()

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 bindToInterfaceFn call, got %d: %+v", len(*calls), *calls)
	}
	if (*calls)[0].ifaceName != "eth0" || !(*calls)[0].isIPv6 {
		t.Errorf("unexpected call: %+v", (*calls)[0])
	}
}

// TestStart_NoInterface_StillDispatchesWithEmptyName documents that Start()
// always calls the seam (so the platform layer, not Start, is the single
// place that decides whether an empty interface name is a no-op).
func TestStart_NoInterface_StillDispatchesWithEmptyName(t *testing.T) {
	calls := spyBindToInterface(t)

	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Close()

	for _, c := range *calls {
		if c.ifaceName != "" {
			t.Errorf("expected empty ifaceName when Interface unset, got %+v", c)
		}
	}
}

// TestOpenHopSocket_V4_DispatchesBindToInterface verifies OpenHopSocket binds
// its v4 send socket to Interface, matching Start()'s behavior so MTR/
// traceroute probes leave via the same physical interface as the main
// pinger.
func TestOpenHopSocket_V4_DispatchesBindToInterface(t *testing.T) {
	calls := spyBindToInterface(t)

	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("8.8.8.8")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Interface = "eth0"
	sock, err := p.OpenHopSocket("8.8.8.8")
	if err != nil {
		t.Fatalf("OpenHopSocket: %v", err)
	}
	defer sock.Close()

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 bindToInterfaceFn call, got %d: %+v", len(*calls), *calls)
	}
	if (*calls)[0].ifaceName != "eth0" || (*calls)[0].isIPv6 {
		t.Errorf("unexpected call: %+v", (*calls)[0])
	}
}

// TestOpenHopSocket_V6_DispatchesBindToInterface mirrors the IPv4 case above
// for an IPv6 destination.
func TestOpenHopSocket_V6_DispatchesBindToInterface(t *testing.T) {
	calls := spyBindToInterface(t)

	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("2001:4860:4860::8888")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Interface = "eth0"
	sock, err := p.OpenHopSocket("2001:4860:4860::8888")
	if err != nil {
		t.Fatalf("OpenHopSocket: %v", err)
	}
	defer sock.Close()

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 bindToInterfaceFn call, got %d: %+v", len(*calls), *calls)
	}
	if (*calls)[0].ifaceName != "eth0" || !(*calls)[0].isIPv6 {
		t.Errorf("unexpected call: %+v", (*calls)[0])
	}
}

func TestListenICMPNetworkAndBindAddress(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		v6          bool
		wantNetwork string
		wantAddr    string
	}{
		{"v4 wildcard", "", false, "ip4:icmp", "0.0.0.0"},
		{"v6 wildcard", "", true, "ip6:ipv6-icmp", "::"},
		{"v4 source", "192.0.2.1", false, "ip4:icmp", "192.0.2.1"},
		{"v6 source", "2001:db8::1", true, "ip6:ipv6-icmp", "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotNetwork, gotAddr string
			p := NewPingerWithOptions(nil, Options{
				ListenPacket: func(network, address string) (net.PacketConn, error) {
					gotNetwork, gotAddr = network, address
					return nil, errors.New("stop here")
				},
			})
			p.Source = tt.source

			_, err := p.listenICMP(tt.v6)

			if err == nil {
				t.Fatal("expected the listen error to propagate")
			}
			if gotNetwork != tt.wantNetwork || gotAddr != tt.wantAddr {
				t.Fatalf("listenPacket(%q, %q), want (%q, %q)", gotNetwork, gotAddr, tt.wantNetwork, tt.wantAddr)
			}
		})
	}
}

type bufferPacketConn struct {
	net.PacketConn
	readSize, writeSize int
	readErr, writeErr   error
	closed              bool
}

func (c *bufferPacketConn) SetReadBuffer(size int) error  { c.readSize = size; return c.readErr }
func (c *bufferPacketConn) SetWriteBuffer(size int) error { c.writeSize = size; return c.writeErr }
func (c *bufferPacketConn) Close() error                  { c.closed = true; return nil }

func TestListenICMPConfiguresLargePacketBuffers(t *testing.T) {
	for _, failure := range []string{"", "receive", "send"} {
		t.Run("failure="+failure, func(t *testing.T) {
			conn := &bufferPacketConn{}
			wantErr := errors.New("buffer option failed")
			if failure == "receive" {
				conn.readErr = wantErr
			}
			if failure == "send" {
				conn.writeErr = wantErr
			}
			p := NewPingerWithOptions(nil, Options{ListenPacket: func(string, string) (net.PacketConn, error) { return conn, nil }})
			defer p.Stop()
			got, err := p.listenICMP(true)
			if failure != "" {
				if !errors.Is(err, wantErr) || !conn.closed || got != nil {
					t.Fatalf("got=%v err=%v closed=%v", got, err, conn.closed)
				}
				return
			}
			if err != nil || got != conn || conn.readSize < 2*receiverBufferSize || conn.writeSize < 2*receiverBufferSize {
				t.Fatalf("got=%v err=%v buffers=%d/%d", got, err, conn.readSize, conn.writeSize)
			}
			got.Close()
		})
	}
}
