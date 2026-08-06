//go:build darwin

package pinger

import (
	"fmt"
	"net"
	"testing"
)

// TestBindToInterface_Darwin exercises the real (non-seamed) platform
// implementation end to end: it must never panic, regardless of whether the
// interface name is empty, doesn't exist, or resolves successfully, and
// regardless of address family. Actually verifying that IP_BOUND_IF /
// IPV6_BOUND_IF took effect would require a privileged raw socket and
// packets on the wire, which this suite deliberately avoids (see
// bindif_test.go's dispatch-seam tests for the privilege-free coverage of
// call sites); this test instead covers the non-fatal error-handling
// contract Start()/OpenHopSocket() rely on.
func TestBindToInterface_Darwin(t *testing.T) {
	origLookup := interfaceByNameFn
	t.Cleanup(func() { interfaceByNameFn = origLookup })

	tests := []struct {
		name      string
		ifaceName string
		lookup    func(string) (*net.Interface, error)
		isIPv6    bool
	}{
		{
			name:      "empty interface name is a no-op",
			ifaceName: "",
		},
		{
			name:      "interface lookup failure does not panic",
			ifaceName: "does-not-exist-9999",
			lookup: func(string) (*net.Interface, error) {
				return nil, fmt.Errorf("route: no such network interface")
			},
		},
		{
			name:      "valid interface, ipv4",
			ifaceName: "lo0",
			lookup: func(name string) (*net.Interface, error) {
				return &net.Interface{Index: 1, Name: name}, nil
			},
		},
		{
			name:      "valid interface, ipv6",
			ifaceName: "lo0",
			isIPv6:    true,
			lookup: func(name string) (*net.Interface, error) {
				return &net.Interface{Index: 1, Name: name}, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.lookup != nil {
				interfaceByNameFn = tt.lookup
			} else {
				interfaceByNameFn = origLookup
			}

			conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatalf("open test socket: %v", err)
			}
			defer conn.Close()

			// The call must never panic and must return regardless of
			// whether the underlying setsockopt succeeds (it won't, for a
			// UDP loopback socket asked to bind to a bogus interface index
			// via an ICMP-oriented option) — errors are intentionally
			// swallowed by bindToInterface.
			bindToInterface(conn, tt.ifaceName, tt.isIPv6)
		})
	}
}

// TestBindToInterface_Darwin_NonSyscallConn verifies that a net.PacketConn
// which doesn't expose SyscallConn (e.g. the fakes used in seam-based unit
// tests elsewhere in this package) is handled gracefully rather than via a
// failed type assertion panic.
func TestBindToInterface_Darwin_NonSyscallConn(t *testing.T) {
	bindToInterface(&fakeNetPacketConn{}, "lo0", false)
}
