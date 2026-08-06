//go:build linux

package pinger

import (
	"net"
	"testing"
)

// TestBindToInterface_Linux exercises the real (non-seamed) platform
// implementation end to end: it must never panic regardless of interface
// name validity, and it must tolerate running unprivileged (SO_BINDTODEVICE
// requires CAP_NET_RAW; CI runners are typically unprivileged, so the
// setsockopt call is expected to fail there — that failure is intentionally
// swallowed by bindToInterface, exactly like the pmtu_linux.go /
// pmtu_darwin.go non-fatal socket option calls this feature is modeled on).
func TestBindToInterface_Linux(t *testing.T) {
	tests := []struct {
		name      string
		ifaceName string
	}{
		{"empty interface name is a no-op", ""},
		{"invalid interface name does not panic", "does-not-exist-9999"},
		{"loopback interface", "lo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatalf("open test socket: %v", err)
			}
			defer conn.Close()

			bindToInterface(conn, tt.ifaceName, false)
		})
	}
}

// TestBindToInterface_Linux_NonSyscallConn verifies that a net.PacketConn
// which doesn't expose SyscallConn is handled gracefully rather than via a
// failed type assertion panic.
func TestBindToInterface_Linux_NonSyscallConn(t *testing.T) {
	bindToInterface(&fakeNetPacketConn{}, "lo", false)
}
