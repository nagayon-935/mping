//go:build darwin

package pinger

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
)

type dfPacketConn struct {
	fakeNetPacketConn
	raw        syscall.RawConn
	syscallErr error
	closed     bool
}

func (c *dfPacketConn) SyscallConn() (syscall.RawConn, error) { return c.raw, c.syscallErr }
func (c *dfPacketConn) Close() error                          { c.closed = true; return nil }

type dfRawConn struct {
	syscall.RawConn
	controlErr error
}

func (c dfRawConn) Control(f func(uintptr)) error {
	if c.controlErr != nil {
		return c.controlErr
	}
	// An invalid descriptor makes the real setsockopt fail without opening
	// or binding a socket, so this test needs no privileges.
	f(^uintptr(0))
	return nil
}

func TestCanSendPayload_DontFragmentErrors(t *testing.T) {
	syscallErr := errors.New("SyscallConn failed")
	controlErr := errors.New("Control failed")
	for _, tt := range []struct {
		name string
		conn *dfPacketConn
		want error
	}{
		{"SyscallConn", &dfPacketConn{syscallErr: syscallErr}, syscallErr},
		{"Control", &dfPacketConn{raw: dfRawConn{controlErr: controlErr}}, controlErr},
		{"setsockopt", &dfPacketConn{raw: dfRawConn{}}, syscall.EBADF},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := &fakeRawConn{}
			orig := newRawConnFn
			t.Cleanup(func() { newRawConnFn = orig })
			newRawConnFn = func(net.PacketConn) (pmtuRawConn, error) { return raw, nil }
			p := NewPingerWithOptions(nil, Options{ListenPacket: listenWith(tt.conn)})

			ok, hop, err := p.canSendPayload(context.Background(), &net.IPAddr{IP: net.ParseIP("192.0.2.1")}, 56, 1234)

			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			if ok || hop != "" {
				t.Errorf("result = (%v, %q), want false and no bottleneck", ok, hop)
			}
			if raw.header != nil {
				t.Error("probe sent after DF setup failed")
			}
			if !tt.conn.closed {
				t.Error("socket left open after DF setup failed")
			}
		})
	}
}
