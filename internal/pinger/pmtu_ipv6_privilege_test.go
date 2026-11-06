package pinger

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeIPv6RawConn lets a test run a net.Dialer.Control hook without a socket.
// Every fd-level helper it reaches is stubbed, so the fd is never used.
type fakeIPv6RawConn struct{}

func (fakeIPv6RawConn) Control(f func(fd uintptr)) error  { f(0); return nil }
func (fakeIPv6RawConn) Read(func(fd uintptr) bool) error  { return nil }
func (fakeIPv6RawConn) Write(func(fd uintptr) bool) error { return nil }

// privilegedDialStub records the state observed by stubPrivilegedIPv6Dial.
type privilegedDialStub struct {
	inside     bool     // a runPrivileged section is currently active
	reentries  int      // runPrivileged was entered while already active
	heldBinds  []string // interfaces bound via bindRawConnToInterfaceHeldFn
	bindInside []bool   // whether each held bind ran inside a section
}

// stubPrivilegedIPv6Dial replaces every package seam the IPv6 PMTU dial
// touches. runPrivileged counts reentry, so any nested privileged section on
// any path inside the dial (not only one specific helper) is detected. The
// dial runs the dialer's real Control hook and, like net.Dialer, aborts when
// Control fails. The fd-level helpers are stubbed, so no syscall runs.
//
// The seams are package globals: t.Setenv makes this helper panic if the test
// (or an ancestor) calls t.Parallel, instead of racing with other tests.
func stubPrivilegedIPv6Dial(t *testing.T, dialResult func(s *privilegedDialStub) (net.Conn, error)) *privilegedDialStub {
	t.Helper()
	t.Setenv("MPING_TEST_GLOBAL_SEAMS", "serial")
	origRun, origDial := runPrivileged, dialIPv6PMTUConn
	origHeld, origOpt := bindRawConnToInterfaceHeldFn, setIPv6PMTUDiscoveryFn
	t.Cleanup(func() {
		runPrivileged, dialIPv6PMTUConn = origRun, origDial
		bindRawConnToInterfaceHeldFn, setIPv6PMTUDiscoveryFn = origHeld, origOpt
	})
	s := &privilegedDialStub{}
	runPrivileged = func(fn func() error) error {
		if s.inside {
			s.reentries++
			return fn()
		}
		s.inside = true
		defer func() { s.inside = false }()
		return fn()
	}
	bindRawConnToInterfaceHeldFn = func(_ syscall.RawConn, iface string, _ bool) {
		s.heldBinds = append(s.heldBinds, iface)
		s.bindInside = append(s.bindInside, s.inside)
	}
	setIPv6PMTUDiscoveryFn = func(int) error { return nil }
	dialIPv6PMTUConn = func(_ context.Context, d *net.Dialer, address string) (net.Conn, error) {
		if d.Control != nil {
			if err := d.Control("ip6", address, fakeIPv6RawConn{}); err != nil {
				return nil, err
			}
		}
		return dialResult(s)
	}
	return s
}

var testIPv6Dst = &net.IPAddr{IP: net.ParseIP("2001:db8::1")}

// TestOpenIPv6PMTUSocket_DialsInsidePrivilegedSection guards against the
// setuid failure seen on macOS: the raw ICMPv6 PMTU socket is created after
// ping has started, concurrently with AsRealUser sections (e.g. opening the
// browser for --web), so its socket() call must wait for privileges exactly
// like privilegedListenPacket does.
func TestOpenIPv6PMTUSocket_DialsInsidePrivilegedSection(t *testing.T) {
	// Arrange
	errDial := errors.New("dial stopped by test")
	dialedInside := false
	stubPrivilegedIPv6Dial(t, func(s *privilegedDialStub) (net.Conn, error) {
		dialedInside = s.inside
		return nil, errDial
	})
	p := NewPingerWithOptions(nil, Options{})

	// Act
	_, err := openIPv6PMTUSocket(context.Background(), p, testIPv6Dst)

	// Assert
	if !errors.Is(err, errDial) {
		t.Fatalf("err = %v, want the dial error", err)
	}
	if !dialedInside {
		t.Fatal("IPv6 PMTU socket was dialled outside privilege.Privileged")
	}
}

// TestOpenIPv6PMTUSocket_DoesNotReenterPrivilegedSection guards against a
// deadlock: privilege's RWMutex is not reentrant, so a second read lock taken
// anywhere inside the privileged dial blocks forever once an AsRealUser
// section is waiting for the write lock in between.
func TestOpenIPv6PMTUSocket_DoesNotReenterPrivilegedSection(t *testing.T) {
	// Arrange
	s := stubPrivilegedIPv6Dial(t, func(*privilegedDialStub) (net.Conn, error) { return nil, errors.New("stop") })
	p := NewPingerWithOptions(nil, Options{})
	p.Interface = "en0"

	// Act
	_, _ = openIPv6PMTUSocket(context.Background(), p, testIPv6Dst)

	// Assert
	if s.reentries != 0 {
		t.Fatalf("privileged section re-entered %d time(s) inside the dial", s.reentries)
	}
	if len(s.heldBinds) != 1 || s.heldBinds[0] != "en0" || !s.bindInside[0] {
		t.Fatalf("held binds = %v (inside=%v), want one en0 bind inside the section", s.heldBinds, s.bindInside)
	}
}

// TestOpenIPv6PMTUSocket_ControlFailureAbortsDial verifies a failing socket
// option is reported, as net.Dialer aborts the dial when Control fails.
func TestOpenIPv6PMTUSocket_ControlFailureAbortsDial(t *testing.T) {
	// Arrange
	errOption := errors.New("cannot prohibit fragmentation")
	dialed := false
	stubPrivilegedIPv6Dial(t, func(*privilegedDialStub) (net.Conn, error) { dialed = true; return nil, nil })
	setIPv6PMTUDiscoveryFn = func(int) error { return errOption }
	p := NewPingerWithOptions(nil, Options{})

	// Act
	_, err := openIPv6PMTUSocket(context.Background(), p, testIPv6Dst)

	// Assert
	if !errors.Is(err, errOption) || dialed {
		t.Fatalf("err = %v, dialed = %v; want the option error and no connection", err, dialed)
	}
}

// TestOpenIPv6PMTUSocket_RejectsNonIPConn verifies an unexpected connection
// type is reported and closed instead of panicking.
func TestOpenIPv6PMTUSocket_RejectsNonIPConn(t *testing.T) {
	// Arrange
	local, remote := net.Pipe()
	t.Cleanup(func() { _ = remote.Close() })
	stubPrivilegedIPv6Dial(t, func(*privilegedDialStub) (net.Conn, error) { return local, nil })
	p := NewPingerWithOptions(nil, Options{})

	// Act
	_, err := openIPv6PMTUSocket(context.Background(), p, testIPv6Dst)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "unexpected connection type") {
		t.Fatalf("err = %v, want an unexpected connection type error", err)
	}
	_ = local.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	if _, werr := local.Write([]byte{0}); !errors.Is(werr, io.ErrClosedPipe) {
		t.Fatalf("write after rejection = %v, want io.ErrClosedPipe (connection closed)", werr)
	}
}

// TestPrivilegedHelpers_GoThroughRunPrivileged makes reentry detection
// complete: every privileged helper in this package must use the
// runPrivileged seam, so a nested call through any of them is visible.
func TestPrivilegedHelpers_GoThroughRunPrivileged(t *testing.T) {
	tests := []struct {
		name string
		call func()
	}{
		{"bindRawConnToInterfaceFn", func() { bindRawConnToInterfaceFn(fakeIPv6RawConn{}, "", false) }},
		{"bindToInterfaceFn", func() { bindToInterfaceFn(&fakeNetPacketConn{}, "", false) }},
		{"privilegedListenPacket", func() { _, _ = privilegedListenPacket("bogus-network", "") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := stubPrivilegedIPv6Dial(t, func(*privilegedDialStub) (net.Conn, error) { return nil, nil })
			calls := 0
			inner := runPrivileged
			runPrivileged = func(fn func() error) error { calls++; return inner(fn) }
			_ = s

			// Act
			tt.call()

			// Assert
			if calls != 1 {
				t.Fatalf("runPrivileged calls = %d, want 1", calls)
			}
		})
	}
}
