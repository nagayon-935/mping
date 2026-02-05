package pinger

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/nagayon-935/mping/internal/stats"
)

type fakePacketConn struct {
	readQueue []readResult
}

type readResult struct {
	data []byte
	cm   *ipv4.ControlMessage
	addr net.Addr
	err  error
}

func (f *fakePacketConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	if len(f.readQueue) == 0 {
		return 0, nil, nil, timeoutOpError()
	}
	r := f.readQueue[0]
	f.readQueue = f.readQueue[1:]
	n := copy(b, r.data)
	return n, r.cm, r.addr, r.err
}

func (f *fakePacketConn) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) {
	return len(b), nil
}

func (f *fakePacketConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (f *fakePacketConn) Close() error {
	return nil
}

func (f *fakePacketConn) SetControlMessage(cf ipv4.ControlFlags, on bool) error {
	return nil
}

type fakeErrPacketConn struct {
	err error
}

func (f *fakeErrPacketConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	return 0, nil, nil, timeoutOpError()
}

func (f *fakeErrPacketConn) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) {
	return 0, f.err
}

func (f *fakeErrPacketConn) SetReadDeadline(t time.Time) error { return nil }
func (f *fakeErrPacketConn) Close() error                     { return nil }
func (f *fakeErrPacketConn) SetControlMessage(cf ipv4.ControlFlags, on bool) error {
	return nil
}

type fakeNetPacketConn struct{}

func (f *fakeNetPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	return 0, &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, timeoutOpError()
}

func (f *fakeNetPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	return len(b), nil
}

func (f *fakeNetPacketConn) Read(b []byte) (int, error)  { return 0, timeoutOpError() }
func (f *fakeNetPacketConn) Write(b []byte) (int, error) { return len(b), nil }

func (f *fakeNetPacketConn) Close() error                 { return nil }
func (f *fakeNetPacketConn) LocalAddr() net.Addr          { return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (f *fakeNetPacketConn) RemoteAddr() net.Addr         { return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (f *fakeNetPacketConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeNetPacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeNetPacketConn) SetWriteDeadline(t time.Time) error { return nil }

type traceRespKind int

const (
	traceRespTimeout traceRespKind = iota
	traceRespTimeExceeded
	traceRespEchoReply
	traceRespUnreachable
)

type traceResp struct {
	kind traceRespKind
	addr net.Addr
}

type fakeTracePacketConn struct {
	responses []traceResp
	lastID    int
	lastSeq   int
}

func (f *fakeTracePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if len(f.responses) == 0 {
		return 0, nil, timeoutOpError()
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	if resp.kind == traceRespTimeout {
		return 0, nil, timeoutOpError()
	}

	var msg icmp.Message
	switch resp.kind {
	case traceRespEchoReply:
		msg = icmp.Message{
			Type: ipv4.ICMPTypeEchoReply,
			Code: 0,
			Body: &icmp.Echo{ID: f.lastID, Seq: f.lastSeq, Data: []byte("x")},
		}
	case traceRespUnreachable:
		msg = icmp.Message{
			Type: ipv4.ICMPTypeDestinationUnreachable,
			Code: 1,
			Body: &icmp.DstUnreach{Data: buildInnerEchoDataV4(f.lastID, f.lastSeq)},
		}
	case traceRespTimeExceeded:
		msg = icmp.Message{
			Type: ipv4.ICMPTypeTimeExceeded,
			Code: 0,
			Body: &icmp.TimeExceeded{Data: buildInnerEchoDataV4(f.lastID, f.lastSeq)},
		}
	default:
		return 0, nil, timeoutOpError()
	}

	raw, err := msg.Marshal(nil)
	if err != nil {
		return 0, nil, err
	}
	n := copy(b, raw)
	return n, resp.addr, nil
}

func (f *fakeTracePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if len(b) >= 8 {
		f.lastID = int(b[4])<<8 | int(b[5])
		f.lastSeq = int(b[6])<<8 | int(b[7])
	}
	return len(b), nil
}

func (f *fakeTracePacketConn) Read(b []byte) (int, error)  { return 0, timeoutOpError() }
func (f *fakeTracePacketConn) Write(b []byte) (int, error) { return len(b), nil }
func (f *fakeTracePacketConn) Close() error                { return nil }
func (f *fakeTracePacketConn) LocalAddr() net.Addr         { return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (f *fakeTracePacketConn) RemoteAddr() net.Addr        { return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (f *fakeTracePacketConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeTracePacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeTracePacketConn) SetWriteDeadline(t time.Time) error { return nil }

func buildInnerEchoDataV4(id, seq int) []byte {
	echo := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("x")},
	}
	inner, err := echo.Marshal(nil)
	if err != nil {
		return nil
	}
	ipHeader := make([]byte, 20)
	ipHeader[0] = 0x45
	return append(ipHeader, inner...)
}
type fakePacketConnV6 struct {
	readQueue []readResultV6
}

type readResultV6 struct {
	data []byte
	cm   *ipv6.ControlMessage
	addr net.Addr
	err  error
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

func (f *fakePacketConnV6) WriteTo(b []byte, cm *ipv6.ControlMessage, dst net.Addr) (int, error) {
	return len(b), nil
}

func (f *fakePacketConnV6) SetReadDeadline(t time.Time) error {
	return nil
}

func (f *fakePacketConnV6) Close() error {
	return nil
}

func (f *fakePacketConnV6) SetControlMessage(cf ipv6.ControlFlags, on bool) error {
	return nil
}

func timeoutOpError() error {
	return &net.OpError{
		Err: &net.DNSError{IsTimeout: true},
	}
}

func TestRunReceiverDispatchesReply(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})

	id := p.baseID & 0xffff
	reply := icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  7,
			Data: []byte("x"),
		},
	}
	raw, err := reply.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	fake := &fakePacketConn{
		readQueue: []readResult{
			{data: raw, cm: &ipv4.ControlMessage{TTL: 55}},
		},
	}
	p.connV4 = fake
	p.targetMap[id] = target
	p.targetChans[id] = make(chan Reply, 1)

	done := make(chan struct{})
	go func() {
		p.runReceiverV4()
		close(done)
	}()

	select {
	case got := <-p.targetChans[id]:
		if got.TTL != 55 || got.Seq != 7 {
			t.Fatalf("reply mismatch: got ttl=%d seq=%d", got.TTL, got.Seq)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for reply")
	}
	p.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("receiver did not stop")
	}
}

func TestRunReceiverDispatchesICMPError(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})

	id := p.baseID & 0xffff
	echo := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  9,
			Data: []byte("x"),
		},
	}
	inner, err := echo.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	ipHeader := make([]byte, 20)
	ipHeader[0] = 0x45
	data := append(ipHeader, inner...)

	unreach := icmp.Message{
		Type: ipv4.ICMPTypeDestinationUnreachable,
		Code: 1,
		Body: &icmp.DstUnreach{Data: data},
	}
	raw, err := unreach.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	fake := &fakePacketConn{
		readQueue: []readResult{
			{data: raw, cm: &ipv4.ControlMessage{TTL: 55}},
		},
	}
	p.connV4 = fake
	p.targetMap[id] = target
	p.targetChans[id] = make(chan Reply, 1)

	done := make(chan struct{})
	go func() {
		p.runReceiverV4()
		close(done)
	}()

	select {
	case got := <-p.targetChans[id]:
		if got.Seq != 9 {
			t.Fatalf("seq mismatch: got %d, want 9", got.Seq)
		}
		if got.Err == "" {
			t.Fatal("expected error string")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for reply")
	}
	p.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("receiver did not stop")
	}
}

func TestRunReceiverDispatchesReplyV6(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})

	id := p.baseID & 0xffff
	reply := icmp.Message{
		Type: ipv6.ICMPTypeEchoReply,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  7,
			Data: []byte("x"),
		},
	}
	raw, err := reply.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	fake := &fakePacketConnV6{
		readQueue: []readResultV6{
			{data: raw, cm: &ipv6.ControlMessage{HopLimit: 42}},
		},
	}
	p.connV6 = fake
	p.targetMap[id] = target
	p.targetChans[id] = make(chan Reply, 1)

	done := make(chan struct{})
	go func() {
		p.runReceiverV6()
		close(done)
	}()

	select {
	case got := <-p.targetChans[id]:
		if got.TTL != 42 || got.Seq != 7 {
			t.Fatalf("reply mismatch: got ttl=%d seq=%d", got.TTL, got.Seq)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for reply")
	}
	p.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("receiver did not stop")
	}
}

func TestRunReceiverDispatchesICMPErrorV6(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})

	id := p.baseID & 0xffff
	echo := icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  9,
			Data: []byte("x"),
		},
	}
	inner, err := echo.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	data := make([]byte, 40)
	data[0] = 0x60 // IPv6 version
	data = append(data, inner...)

	unreach := icmp.Message{
		Type: ipv6.ICMPTypeDestinationUnreachable,
		Code: 1,
		Body: &icmp.DstUnreach{Data: data},
	}
	raw, err := unreach.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	fake := &fakePacketConnV6{
		readQueue: []readResultV6{
			{data: raw, cm: &ipv6.ControlMessage{HopLimit: 55}},
		},
	}
	p.connV6 = fake
	p.targetMap[id] = target
	p.targetChans[id] = make(chan Reply, 1)

	done := make(chan struct{})
	go func() {
		p.runReceiverV6()
		close(done)
	}()

	select {
	case got := <-p.targetChans[id]:
		if got.Seq != 9 {
			t.Fatalf("seq mismatch: got %d, want 9", got.Seq)
		}
		if got.Err == "" {
			t.Fatal("expected error string")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for reply")
	}
	p.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("receiver did not stop")
	}
}

func TestStartWithIPv4Source(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Source = "1.1.1.1"
	if err := p.Start(false, 10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	if p.connV4 == nil {
		t.Fatal("expected connV4")
	}
	if p.connV6 != nil {
		t.Fatal("expected connV6 nil")
	}
	p.Close()
}

func TestStartDualStack(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	if err := p.Start(false, 10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	if p.connV4 == nil || p.connV6 == nil {
		t.Fatal("expected both v4 and v6 conns")
	}
	p.Close()
}

func TestStartIPv6Only(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Source = "2001:db8::1"
	if err := p.Start(false, 10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("start: %v", err)
	}
	if p.connV6 == nil {
		t.Fatal("expected connV6")
	}
	if p.connV4 != nil {
		t.Fatal("expected connV4 nil")
	}
	p.Close()
}

func TestStartListenError(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, errors.New("listen failed")
		},
	})
	if err := p.Start(false, 10*time.Millisecond, 10*time.Millisecond); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewPingerDefaults(t *testing.T) {
	p := NewPinger(nil)
	if p == nil {
		t.Fatal("expected pinger instance")
	}
	if p.Size != 56 {
		t.Fatalf("default Size: got %d", p.Size)
	}
	if p.ResolveInterval <= 0 {
		t.Fatalf("expected positive ResolveInterval, got %v", p.ResolveInterval)
	}
	if p.resolveIPAddr == nil || p.now == nil || p.listenPacket == nil {
		t.Fatal("expected default option funcs to be set")
	}
	if p.done == nil {
		t.Fatal("expected done channel")
	}
}

func TestWaitBlocksUntilDone(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	p.wg.Add(1)
	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		p.wg.Done()
		close(done)
	}()
	p.Wait()
	select {
	case <-done:
	default:
		t.Fatal("Wait returned before wg done")
	}
}

func TestDiscoverMaxPayloadErrors(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	if _, err := p.DiscoverMaxPayload("", 1, 0, false, nil); err == nil {
		t.Fatal("expected error for empty dest")
	}
	if _, err := p.DiscoverMaxPayload("example.com", 0, 0, false, nil); err == nil {
		t.Fatal("expected error for start <= 0")
	}
}

func TestDiscoverMaxPayloadIPv6NotSupported(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("2001:db8::1")}, nil
		},
	})
	size, err := p.DiscoverMaxPayload("example.com", 100, 0, false, nil)
	if err == nil {
		t.Fatal("expected error for ipv6")
	}
	if size != p.Size {
		t.Fatalf("expected size %d, got %d", p.Size, size)
	}
}

func TestDiscoverMaxPayloadBinarySearch(t *testing.T) {
	orig := canSendPayloadFn
	t.Cleanup(func() { canSendPayloadFn = orig })

	canSendPayloadFn = func(p *Pinger, dst *net.IPAddr, payloadLen int) (bool, error) {
		return payloadLen <= 100, nil
	}

	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
		},
	})
	got, err := p.DiscoverMaxPayload("example.com", 200, 0, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}
}

func TestDiscoverMaxPayloadCanSendError(t *testing.T) {
	orig := canSendPayloadFn
	t.Cleanup(func() { canSendPayloadFn = orig })

	canSendPayloadFn = func(p *Pinger, dst *net.IPAddr, payloadLen int) (bool, error) {
		return false, errors.New("send failed")
	}

	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
		},
	})
	if _, err := p.DiscoverMaxPayload("example.com", 100, 0, false, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestCanSendPayloadListenError(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, errors.New("listen failed")
		},
	})
	_, err := p.canSendPayload(&net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCanSendPayloadRawConnError(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	_, err := p.canSendPayload(&net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, -1)
	if err == nil {
		t.Fatal("expected error from raw conn")
	}
}

func TestTraceRouteInvalidInputs(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	if _, err := p.TraceRoute("", 30, 1*time.Second); err == nil {
		t.Fatal("expected error for empty dest")
	}
	if _, err := p.TraceRoute("example.com", 0, 1*time.Second); err == nil {
		t.Fatal("expected error for maxHops <= 0")
	}
}

func TestTraceRouteResolveError(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return nil, errors.New("resolve failed")
		},
	})
	if _, err := p.TraceRoute("example.com", 3, 1*time.Second); err == nil {
		t.Fatal("expected resolve error")
	}
}

func TestTraceRouteListenErrorIPv4(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, errors.New("listen failed")
		},
	})
	if _, err := p.TraceRoute("example.com", 3, 1*time.Second); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestTraceRouteListenErrorIPv6(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("2001:db8::1")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return nil, errors.New("listen failed")
		},
	})
	if _, err := p.TraceRoute("example.com", 3, 1*time.Second); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestTraceRouteIPv4Timeouts(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})

	hops, err := p.TraceRoute("example.com", 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("TraceRoute: %v", err)
	}
	if len(hops) != 3 {
		t.Fatalf("expected 3 hops, got %d", len(hops))
	}
	for i, hop := range hops {
		if hop != "*" {
			t.Fatalf("hop %d: expected '*', got %q", i, hop)
		}
	}
}

func TestLogWritesCSV(t *testing.T) {
	var buf bytes.Buffer
	p := NewPingerWithOptions([]*stats.TargetStats{stats.NewTargetStats("example.com")}, Options{})
	p.LogWriter = &buf
	p.log(p.Targets[0], 1, "OK", 10*time.Millisecond, 64, "")
	if !strings.Contains(buf.String(), "OK") {
		t.Fatalf("expected log output")
	}
}

func TestSetLastErrNil(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	p.setLastErr(nil)
	if p.lastErrMsg != "" {
		t.Fatalf("expected empty lastErrMsg")
	}
}

func TestParseInnerEchoIDSeqShortData(t *testing.T) {
	if _, _, ok := parseInnerEchoIDSeq([]byte{0x40}); ok {
		t.Fatal("expected false for short data")
	}
}

func TestExtractEchoIDSeqUnknown(t *testing.T) {
	msg := icmp.Message{Type: ipv4.ICMPTypeEchoReply}
	if _, _, ok := extractEchoIDSeq(&msg); ok {
		t.Fatal("expected false for unsupported type")
	}
}

func TestRunWorkerSuccessPath(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ResolveIPAddr: resolve,
	})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	id := p.baseID & 0xffff
	ch := make(chan Reply, 1)
	p.targetChans[id] = ch

	go func() {
		time.Sleep(10 * time.Millisecond)
		ch <- Reply{TTL: 64, Seq: 1}
	}()

	p.runWorker(target, id, 10*time.Millisecond, 200*time.Millisecond)

	view := target.GetView()
	if view.Recv != 1 {
		t.Fatalf("Recv: got %d, want 1", view.Recv)
	}
	if view.LastTTL != 64 {
		t.Fatalf("LastTTL: got %d, want 64", view.LastTTL)
	}
	if view.LastRTT == 0 {
		t.Fatalf("LastRTT should be set")
	}
}

func TestBuildPayload(t *testing.T) {
	got := buildPayload(8)
	if len(got) != 8 {
		t.Fatalf("payload len: got %d", len(got))
	}
	if string(got[:5]) != "MPING" {
		t.Fatalf("signature: got %q", string(got[:5]))
	}
	if got[5] != 'A' {
		t.Fatalf("payload fill: got %q", got[5])
	}
}

func TestApplyLastErrSource(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	p.Source = "10.0.0.2"
	p.setLastErr(net.ErrClosed)

	msg := p.applyLastErrSource("write ip 0.0.0.0->8.8.8.8: sendmsg: no route to host")
	if msg == "" || msg == "write ip 0.0.0.0->8.8.8.8: sendmsg: no route to host" {
		t.Fatalf("expected source ip substitution, got %q", msg)
	}
}

func TestApplyLastErrSource_UsesLastErr(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	p.Source = "10.0.0.2"
	last := "write ip 0.0.0.0->1.1.1.1: sendmsg: no route to host"
	p.setLastErr(errors.New(last))
	msg := p.applyLastErrSource(last)
	if msg == last {
		t.Fatalf("expected replacement, got %q", msg)
	}
}

func TestApplyLastErrSource_NoSource(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	msg := p.applyLastErrSource("write ip 0.0.0.0->1.1.1.1: x")
	if msg != "write ip 0.0.0.0->1.1.1.1: x" {
		t.Fatalf("unexpected change: %q", msg)
	}
}

func TestICMPErrorStrings(t *testing.T) {
	if icmpErrorString(ipv4.ICMPTypeDestinationUnreachable, 1) == "" {
		t.Fatal("expected v4 dest unreachable string")
	}
	if icmpErrorString(ipv4.ICMPTypeTimeExceeded, 0) != "Time Exceeded" {
		t.Fatal("expected Time Exceeded")
	}
	if icmpErrorString(ipv4.ICMPTypeParameterProblem, 2) != "Bad Length" {
		t.Fatal("expected Bad Length")
	}
	if icmpErrorString(ipv4.ICMPTypeEchoReply, 0) != "ICMP Error" {
		t.Fatal("expected default ICMP Error")
	}
	if icmpV6ErrorString(ipv6.ICMPTypeDestinationUnreachable, 1) == "" {
		t.Fatal("expected v6 dest unreachable string")
	}
	if icmpV6ErrorString(ipv6.ICMPTypeTimeExceeded, 0) != "Time Exceeded" {
		t.Fatal("expected v6 time exceeded")
	}
	if icmpV6ErrorString(ipv6.ICMPTypeParameterProblem, 0) != "Parameter Problem" {
		t.Fatal("expected v6 parameter problem")
	}
}

func TestIsIPv4(t *testing.T) {
	if !isIPv4("8.8.8.8") {
		t.Fatal("expected IPv4 true")
	}
	if isIPv4("2001:db8::1") {
		t.Fatal("expected IPv4 false")
	}
}

func TestDestUnreachStrings(t *testing.T) {
	for code := 0; code <= 15; code++ {
		if destUnreachString(code) == "" {
			t.Fatalf("expected destUnreachString for code %d", code)
		}
	}
	if destUnreachV6String(0) == "" {
		t.Fatal("expected v6 destUnreach string")
	}
	if destUnreachV6String(1) == "" {
		t.Fatal("expected v6 destUnreach string")
	}
	if destUnreachV6String(3) == "" {
		t.Fatal("expected v6 destUnreach string")
	}
	if destUnreachV6String(4) == "" {
		t.Fatal("expected v6 destUnreach string")
	}
	if destUnreachV6String(99) != "Destination Unreachable" {
		t.Fatal("expected default v6 destUnreach string")
	}
}

func TestParamProblemStringDefaults(t *testing.T) {
	if paramProblemString(0) != "Parameter Problem" {
		t.Fatal("expected default param problem")
	}
	if paramProblemString(1) != "Missing Required Option" {
		t.Fatal("expected missing required option")
	}
	if paramProblemString(2) != "Bad Length" {
		t.Fatal("expected bad length")
	}
}

func TestRunWorkerTimeout(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ResolveIPAddr: resolve,
	})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	p.runWorker(target, id, 5*time.Millisecond, 20*time.Millisecond)

	view := target.GetView()
	if view.Loss != 1 {
		t.Fatalf("Loss: got %d, want 1", view.Loss)
	}
	if view.LastError == "" {
		t.Fatal("expected LastError on timeout")
	}
}

func TestRunWorkerICMPError(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ResolveIPAddr: resolve,
	})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	id := p.baseID & 0xffff
	ch := make(chan Reply, 1)
	p.targetChans[id] = ch

	go func() {
		time.Sleep(10 * time.Millisecond)
		ch <- Reply{Seq: 1, Err: "ICMP Error"}
	}()

	p.runWorker(target, id, 10*time.Millisecond, 200*time.Millisecond)

	view := target.GetView()
	if view.Loss != 1 {
		t.Fatalf("Loss: got %d, want 1", view.Loss)
	}
	if view.LastError == "" {
		t.Fatal("expected LastError for ICMP error")
	}
}

func TestRunWorkerNoConn(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ResolveIPAddr: resolve,
	})
	p.Count = 1

	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	done := make(chan struct{})
	go func() {
		p.runWorker(target, id, 5*time.Millisecond, 50*time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	p.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker did not stop")
	}

	view := target.GetView()
	if view.LastError == "" {
		t.Fatal("expected LastError for no conn")
	}
}

func TestRunWorkerSendError(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ResolveIPAddr: resolve,
	})
	p.connV4 = &fakeErrPacketConn{err: syscall.EPERM}
	p.Count = 1

	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	done := make(chan struct{})
	go func() {
		p.runWorker(target, id, 5*time.Millisecond, 50*time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	p.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker did not stop")
	}

	view := target.GetView()
	if view.LastError == "" {
		t.Fatal("expected LastError for send error")
	}
}

func TestIsMTUTooLarge(t *testing.T) {
	if !isMTUTooLarge(syscall.EMSGSIZE) {
		t.Fatal("expected EMSGSIZE to be MTU too large")
	}
	if isMTUTooLarge(syscall.EINVAL) {
		t.Fatal("expected EINVAL to be false")
	}
}

func TestParseInnerEchoIDSeqIPv4(t *testing.T) {
	echo := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: 42, Seq: 7, Data: []byte("x")},
	}
	inner, err := echo.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	ipHeader := make([]byte, 20)
	ipHeader[0] = 0x45
	data := append(ipHeader, inner...)

	id, seq, ok := parseInnerEchoIDSeq(data)
	if !ok {
		t.Fatal("expected ok")
	}
	if id != 42 || seq != 7 {
		t.Fatalf("id/seq mismatch: %d/%d", id, seq)
	}
}

func TestParseInnerEchoIDSeqIPv6(t *testing.T) {
	echo := icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{ID: 55, Seq: 9, Data: []byte("x")},
	}
	inner, err := echo.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	ipHeader := make([]byte, 40)
	ipHeader[0] = 0x60
	data := append(ipHeader, inner...)

	id, seq, ok := parseInnerEchoIDSeq(data)
	if !ok {
		t.Fatal("expected ok")
	}
	if id != 55 || seq != 9 {
		t.Fatalf("id/seq mismatch: %d/%d", id, seq)
	}
}

func TestTimeExceededString(t *testing.T) {
	if timeExceededString(1) != "Fragment Reassembly Time Exceeded" {
		t.Fatal("expected fragment reassembly")
	}
	if timeExceededString(99) != "Time Exceeded" {
		t.Fatal("expected default time exceeded")
	}
}

func TestExtractEchoIDSeqV4(t *testing.T) {
	echo := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: 12, Seq: 34, Data: []byte("x")},
	}
	inner, err := echo.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	ipHeader := make([]byte, 20)
	ipHeader[0] = 0x45
	data := append(ipHeader, inner...)
	unreach := icmp.Message{
		Type: ipv4.ICMPTypeDestinationUnreachable,
		Code: 1,
		Body: &icmp.DstUnreach{Data: data},
	}
	id, seq, ok := extractEchoIDSeq(&unreach)
	if !ok || id != 12 || seq != 34 {
		t.Fatalf("unexpected extract: ok=%v id=%d seq=%d", ok, id, seq)
	}
}

func TestExtractEchoIDSeqV6(t *testing.T) {
	echo := icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{ID: 12, Seq: 34, Data: []byte("x")},
	}
	inner, err := echo.Marshal(nil)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	ipHeader := make([]byte, 40)
	ipHeader[0] = 0x60
	data := append(ipHeader, inner...)
	unreach := icmp.Message{
		Type: ipv6.ICMPTypeDestinationUnreachable,
		Code: 1,
		Body: &icmp.DstUnreach{Data: data},
	}
	id, seq, ok := extractEchoIDSeq(&unreach)
	if !ok || id != 12 || seq != 34 {
		t.Fatalf("unexpected extract: ok=%v id=%d seq=%d", ok, id, seq)
	}
}
