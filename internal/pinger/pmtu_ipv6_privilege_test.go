package pinger

import (
	"context"
	"errors"
	"net"
	"testing"
)

// TestOpenIPv6PMTUSocket_DialsInsidePrivilegedSection guards against the
// setuid failure seen on macOS: the raw ICMPv6 PMTU socket is created after
// ping has started, concurrently with AsRealUser sections, so its socket()
// call must wait for privileges exactly like privilegedListenPacket does.
func TestOpenIPv6PMTUSocket_DialsInsidePrivilegedSection(t *testing.T) {
	// Arrange
	origRun, origDial := runPrivileged, dialIPv6PMTUConn
	t.Cleanup(func() { runPrivileged, dialIPv6PMTUConn = origRun, origDial })
	inside := false
	runPrivileged = func(fn func() error) error {
		inside = true
		defer func() { inside = false }()
		return fn()
	}
	dialedInside := false
	errDial := errors.New("dial stopped by test")
	dialIPv6PMTUConn = func(context.Context, *net.Dialer, string) (net.Conn, error) {
		dialedInside = inside
		return nil, errDial
	}
	p := NewPingerWithOptions(nil, Options{})

	// Act
	_, err := openIPv6PMTUSocket(context.Background(), p, &net.IPAddr{IP: net.ParseIP("2001:db8::1")})

	// Assert
	if !errors.Is(err, errDial) {
		t.Fatalf("err = %v, want the dial error", err)
	}
	if !dialedInside {
		t.Fatal("IPv6 PMTU socket was dialled outside privilege.Privileged")
	}
}
