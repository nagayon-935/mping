package pinger

import (
	"net"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// This file holds every socket fake used by the package's tests. Raw sockets
// are replaced through the Pinger's function-variable seams, so no test needs
// privileges. Each fake is configured through plain fields rather than by
// subclassing; the default (zero value) behaves like an idle socket that
// accepts every write and times out every read.

func timeoutOpError() error {
	return &net.OpError{
		Err: &net.DNSError{IsTimeout: true},
	}
}

// readResult is one scripted inbound packet for fakePacketConn / fakeHopConn.
type readResult struct {
	data []byte
	cm   *ipv4.ControlMessage
	addr net.Addr
	err  error
}

// fakePacketConn is the IPv4 PacketConnV4 fake.
type fakePacketConn struct {
	mu sync.Mutex

	// readQueue is drained one packet per ReadFrom call. Once empty, ReadFrom
	// returns readErr, or a read timeout when readErr is nil.
	readQueue []readResult
	readErr   error
	// readDelay is slept before an empty-queue ReadFrom returns, so a
	// receiver loop polling this fake doesn't spin hot.
	readDelay time.Duration

	// writeErr fails every WriteTo; deadlineErr fails every SetReadDeadline.
	writeErr    error
	deadlineErr error
	// onWrite runs on every WriteTo (outside the lock) after the payload is
	// recorded. A non-nil result fails the write.
	onWrite func(b []byte, dst net.Addr) error

	// written holds a copy of every WriteTo payload, so tests can parse back
	// what was actually put on the wire.
	written [][]byte
}

func (f *fakePacketConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	f.mu.Lock()
	if len(f.readQueue) == 0 {
		err, delay := f.readErr, f.readDelay
		f.mu.Unlock()
		if err != nil {
			return 0, nil, nil, err
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		return 0, nil, nil, timeoutOpError()
	}
	r := f.readQueue[0]
	f.readQueue = f.readQueue[1:]
	f.mu.Unlock()
	n := copy(b, r.data)
	return n, r.cm, r.addr, r.err
}

func (f *fakePacketConn) WriteTo(b []byte, _ *ipv4.ControlMessage, dst net.Addr) (int, error) {
	f.mu.Lock()
	f.written = append(f.written, append([]byte(nil), b...))
	hook, werr := f.onWrite, f.writeErr
	f.mu.Unlock()
	if hook != nil {
		if err := hook(b, dst); err != nil {
			return 0, err
		}
	}
	if werr != nil {
		return 0, werr
	}
	return len(b), nil
}

func (f *fakePacketConn) SetReadDeadline(time.Time) error { return f.deadlineErr }
func (f *fakePacketConn) Close() error                    { return nil }
func (f *fakePacketConn) SetControlMessage(ipv4.ControlFlags, bool) error {
	return nil
}

// writes returns a snapshot of the recorded WriteTo payloads.
func (f *fakePacketConn) writes() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.written...)
}

// readResultV6 is the IPv6 counterpart of readResult.
type readResultV6 struct {
	data []byte
	cm   *ipv6.ControlMessage
	addr net.Addr
	err  error
}

// fakePacketConnV6 is the IPv6 PacketConnV6 fake.
type fakePacketConnV6 struct {
	readQueue []readResultV6
	writes    int // WriteTo calls
}

func (f *fakePacketConnV6) ReadFrom(b []byte) (int, *ipv6.ControlMessage, net.Addr, error) {
	if len(f.readQueue) == 0 {
		return 0, nil, nil, timeoutOpError()
	}
	r := f.readQueue[0]
	f.readQueue = f.readQueue[1:]
	n := copy(b, r.data)
	return n, r.cm, r.addr, r.err
}

func (f *fakePacketConnV6) WriteTo(b []byte, _ *ipv6.ControlMessage, _ net.Addr) (int, error) {
	f.writes++
	return len(b), nil
}

func (f *fakePacketConnV6) SetReadDeadline(time.Time) error                 { return nil }
func (f *fakePacketConnV6) Close() error                                    { return nil }
func (f *fakePacketConnV6) SetControlMessage(ipv6.ControlFlags, bool) error { return nil }
func (f *fakePacketConnV6) SetICMPFilter(*ipv6.ICMPFilter) error            { return nil }

// fakeNetPacketConn is the net.PacketConn (and net.Conn) fake handed out by
// the Pinger's ListenPacket seam. It is not a real socket, so the ipv4/ipv6
// wrappers built around it are inert; it only has to be a valid return value.
type fakeNetPacketConn struct{}

func (f *fakeNetPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, timeoutOpError()
}
func (f *fakeNetPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (f *fakeNetPacketConn) Read([]byte) (int, error)                  { return 0, timeoutOpError() }
func (f *fakeNetPacketConn) Write(b []byte) (int, error)               { return len(b), nil }
func (f *fakeNetPacketConn) Close() error                              { return nil }
func (f *fakeNetPacketConn) LocalAddr() net.Addr {
	return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}
}
func (f *fakeNetPacketConn) RemoteAddr() net.Addr {
	return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}
}
func (f *fakeNetPacketConn) SetDeadline(time.Time) error      { return nil }
func (f *fakeNetPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeNetPacketConn) SetWriteDeadline(time.Time) error { return nil }

// fakeHopConn implements hopSendConnV4: the send socket a HopSocket holds
// for TTL-limited probes.
type fakeHopConn struct {
	mu sync.Mutex

	// readQueue is drained one packet per ReadFrom call; once empty ReadFrom
	// times out.
	readQueue []readResult
	// deadlineErr fails SetReadDeadline.
	deadlineErr error
	// onWrite runs on every WriteTo; a non-nil result fails the write.
	onWrite func(b []byte, dst net.Addr) error
	// ttlDelay is slept inside SetTTL after the value is stored, widening the
	// window between "set" and "write" so a race between concurrent callers
	// sharing one HopSocket becomes reliably observable.
	ttlDelay time.Duration

	// ttl is the connection-wide TTL (IPv4 has no per-packet TTL); sentTTLs
	// records the value in effect at each WriteTo.
	ttl      int
	sentTTLs []int

	closed bool
}

func (f *fakeHopConn) SetTTL(ttl int) error {
	f.mu.Lock()
	f.ttl = ttl
	delay := f.ttlDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return nil
}

func (f *fakeHopConn) WriteTo(b []byte, _ *ipv4.ControlMessage, dst net.Addr) (int, error) {
	f.mu.Lock()
	f.sentTTLs = append(f.sentTTLs, f.ttl)
	hook := f.onWrite
	f.mu.Unlock()
	if hook != nil {
		if err := hook(b, dst); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

func (f *fakeHopConn) SetReadDeadline(time.Time) error { return f.deadlineErr }

func (f *fakeHopConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeHopConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.readQueue) == 0 {
		return 0, nil, nil, timeoutOpError()
	}
	r := f.readQueue[0]
	f.readQueue = f.readQueue[1:]
	return copy(b, r.data), r.cm, r.addr, r.err
}

// fakeHopConnV6 implements hopSendConnV6; see fakeHopConn.
type fakeHopConnV6 struct {
	readQueue   []readResultV6
	deadlineErr error
	onWrite     func(b []byte, dst net.Addr) error
	closed      bool
}

func (f *fakeHopConnV6) SetHopLimit(int) error { return nil }

func (f *fakeHopConnV6) WriteTo(b []byte, _ *ipv6.ControlMessage, dst net.Addr) (int, error) {
	if f.onWrite != nil {
		if err := f.onWrite(b, dst); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

func (f *fakeHopConnV6) SetReadDeadline(time.Time) error { return f.deadlineErr }

func (f *fakeHopConnV6) Close() error {
	f.closed = true
	return nil
}

func (f *fakeHopConnV6) SetICMPFilter(*ipv6.ICMPFilter) error {
	return nil
}

func (f *fakeHopConnV6) ReadFrom(b []byte) (int, *ipv6.ControlMessage, net.Addr, error) {
	if len(f.readQueue) == 0 {
		return 0, nil, nil, timeoutOpError()
	}
	r := f.readQueue[0]
	f.readQueue = f.readQueue[1:]
	return copy(b, r.data), r.cm, r.addr, r.err
}

// rawRead is one scripted inbound packet for fakeRawConn.
type rawRead struct {
	hdr     *ipv4.Header
	payload []byte
}

// fakeRawConn implements pmtuRawConn: the header-included socket
// canSendPayload probes through.
type fakeRawConn struct {
	// writeErr fails WriteTo; reads is drained one packet per ReadFrom call,
	// and once empty ReadFrom times out.
	writeErr error
	reads    []rawRead

	// header and payload record the last WriteTo.
	header  *ipv4.Header
	payload []byte
}

func (f *fakeRawConn) WriteTo(h *ipv4.Header, p []byte, _ *ipv4.ControlMessage) error {
	f.header, f.payload = h, append([]byte(nil), p...)
	return f.writeErr
}

func (f *fakeRawConn) SetReadDeadline(time.Time) error { return nil }

func (f *fakeRawConn) ReadFrom(b []byte) (*ipv4.Header, []byte, *ipv4.ControlMessage, error) {
	if len(f.reads) == 0 {
		return nil, nil, nil, timeoutOpError()
	}
	r := f.reads[0]
	f.reads = f.reads[1:]
	return r.hdr, b[:copy(b, r.payload)], nil, nil
}
