//go:build !darwin && !linux

package pinger

import "testing"

// TestBindToInterface_Other verifies the no-op fallback for platforms
// without a supported interface-binding facility never touches its
// net.PacketConn argument (proven by passing nil) and never panics,
// regardless of the interface name — preserving the pre-existing
// source-IP-bind-only behavior exactly.
func TestBindToInterface_Other(t *testing.T) {
	bindToInterface(nil, "any-iface", false)
	bindToInterface(nil, "", true)
}
