package pinger

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

// fakeIPv6RawConn lets a test run a net.Dialer.Control hook without a socket.
type fakeIPv6RawConn struct{}

func (fakeIPv6RawConn) Control(f func(fd uintptr)) error  { f(^uintptr(0)); return nil }
func (fakeIPv6RawConn) Read(func(fd uintptr) bool) error  { return nil }
func (fakeIPv6RawConn) Write(func(fd uintptr) bool) error { return nil }

// stubPrivilegedIPv6Dial makes runPrivileged record whether it is active and
// makes the dial run the dialer's real Control hook, then return dialResult.
func stubPrivilegedIPv6Dial(t *testing.T, dialResult func() (net.Conn, error)) (inside *bool) {
	t.Helper()
	origRun, origDial := runPrivileged, dialIPv6PMTUConn
	t.Cleanup(func() { runPrivileged, dialIPv6PMTUConn = origRun, origDial })
	active := false
	runPrivileged = func(fn func() error) error {
		active = true
		defer func() { active = false }()
		return fn()
	}
	dialIPv6PMTUConn = func(_ context.Context, d *net.Dialer, address string) (net.Conn, error) {
		if d.Control != nil {
			_ = d.Control("ip6:ipv6-icmp", address, fakeIPv6RawConn{})
		}
		return dialResult()
	}
	return &active
}

// TestOpenIPv6PMTUSocket_DialsInsidePrivilegedSection guards against the
// setuid failure seen on macOS: the raw ICMPv6 PMTU socket is created after
// ping has started, concurrently with AsRealUser sections (e.g. opening the
// browser for --web), so its socket() call must wait for privileges exactly
// like privilegedListenPacket does.
func TestOpenIPv6PMTUSocket_DialsInsidePrivilegedSection(t *testing.T) {
	// Arrange
	errDial := errors.New("dial stopped by test")
	dialedInside := false
	var inside *bool
	inside = stubPrivilegedIPv6Dial(t, func() (net.Conn, error) {
		dialedInside = *inside
		return nil, errDial
	})
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

// TestOpenIPv6PMTUSocket_ControlDoesNotReenterPrivilegedSection guards
// against a deadlock: privilege's RWMutex is not reentrant, so taking a
// second read lock inside the dial's Control hook blocks forever once an
// AsRealUser section is waiting for the write lock in between.
func TestOpenIPv6PMTUSocket_ControlDoesNotReenterPrivilegedSection(t *testing.T) {
	// Arrange
	inside := stubPrivilegedIPv6Dial(t, func() (net.Conn, error) { return nil, errors.New("stop") })
	origLocking, origHeld := bindRawConnToInterfaceFn, bindRawConnToInterfaceHeldFn
	t.Cleanup(func() { bindRawConnToInterfaceFn, bindRawConnToInterfaceHeldFn = origLocking, origHeld })
	bindRawConnToInterfaceFn = func(syscall.RawConn, string, bool) {
		t.Error("Control re-entered privilege.Privileged via bindRawConnToInterfaceFn")
	}
	var heldCalls []bool
	bindRawConnToInterfaceHeldFn = func(_ syscall.RawConn, iface string, isIPv6 bool) {
		if iface != "en0" || !isIPv6 {
			t.Errorf("bound %q (IPv6=%v), want en0 over IPv6", iface, isIPv6)
		}
		heldCalls = append(heldCalls, *inside)
	}
	p := NewPingerWithOptions(nil, Options{})
	p.Interface = "en0"

	// Act
	_, _ = openIPv6PMTUSocket(context.Background(), p, &net.IPAddr{IP: net.ParseIP("2001:db8::1")})

	// Assert
	if len(heldCalls) != 1 || !heldCalls[0] {
		t.Fatalf("held interface bind calls (inside privileged section) = %v, want exactly one inside", heldCalls)
	}
}

// TestOpenIPv6PMTUSocket_RejectsNonIPConn verifies an unexpected connection
// type is reported and closed instead of panicking.
func TestOpenIPv6PMTUSocket_RejectsNonIPConn(t *testing.T) {
	// Arrange
	local, remote := net.Pipe()
	t.Cleanup(func() { _ = remote.Close() })
	stubPrivilegedIPv6Dial(t, func() (net.Conn, error) { return local, nil })
	p := NewPingerWithOptions(nil, Options{})

	// Act
	_, err := openIPv6PMTUSocket(context.Background(), p, &net.IPAddr{IP: net.ParseIP("2001:db8::1")})

	// Assert
	if err == nil {
		t.Fatal("expected an error for a non-IP connection")
	}
	_ = local.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	if _, werr := local.Write([]byte{0}); !errors.Is(werr, io.ErrClosedPipe) {
		t.Fatalf("write after rejection = %v, want io.ErrClosedPipe (connection closed)", werr)
	}
}
