package pinger

import (
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
// place that decides whether an empty interface name is a no-op) — this
// keeps Start()'s branching identical to before this feature existed.
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
