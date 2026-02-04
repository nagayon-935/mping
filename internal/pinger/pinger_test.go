package pinger

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

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