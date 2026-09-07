package pinger

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
)

func TestStartFallsBackToUnprivilegedICMP(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(string, string) (net.PacketConn, error) {
			return nil, errors.New("raw socket denied")
		},
	})
	p.Source = "127.0.0.1"
	p.listenDatagramV4 = func(string) (PacketConnV4, error) { return &fakePacketConn{}, nil }
	if err := p.Start(time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(); p.Wait(); p.Close() })
	if !p.unprivilegedV4 || p.connV4 == nil {
		t.Fatal("raw socket failure did not fall back to non-privileged ICMP")
	}
}

func TestUnprivilegedReplyUsesPayloadTargetID(t *testing.T) {
	const targetID = 1234
	p := NewPinger(nil)
	ch := make(chan Reply, 1)
	p.targetChans[targetID] = ch
	payload := buildPayload(56)
	embedTargetID(payload, targetID)
	p.handleEchoReply(&icmp.Message{Body: &icmp.Echo{ID: 9999, Seq: 7, Data: payload}}, 64, 0)
	select {
	case reply := <-ch:
		if reply.Seq != 7 {
			t.Fatalf("sequence=%d", reply.Seq)
		}
	default:
		t.Fatal("kernel-rewritten Echo.ID prevented reply dispatch")
	}
}

func TestUnprivilegedICMPRejectsPayloadTooSmallForRouting(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(string, string) (net.PacketConn, error) {
			return nil, errors.New("raw socket denied")
		},
	})
	p.Source = "127.0.0.1"
	p.Size = minTargetIDPayloadSize - 1
	p.listenDatagramV4 = func(string) (PacketConnV4, error) { return &fakePacketConn{}, nil }
	if err := p.Start(time.Second, time.Second); err == nil {
		t.Fatal("expected payload routing size error")
	}
}

func TestUnprivilegedV4ConvertsDestinationToUDPAddr(t *testing.T) {
	dst := unprivilegedDestination(&net.IPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if _, ok := dst.(*net.UDPAddr); !ok {
		t.Fatalf("destination type=%T", dst)
	}
}

func TestUnprivilegedLoopbackPing(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("non-privileged ICMP datagrams are supported on Darwin and Linux")
	}
	target := stats.NewTargetStats("127.0.0.1")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ListenPacket: func(string, string) (net.PacketConn, error) {
			return nil, errors.New("force non-privileged fallback")
		},
	})
	p.Source = "127.0.0.1"
	p.Count = 1
	p.listenDatagramV4 = listenUnprivilegedV4
	if err := p.Start(10*time.Millisecond, time.Second); err != nil {
		t.Skipf("host does not allow non-privileged ICMP: %v", err)
	}
	t.Cleanup(func() { p.Stop(); p.Wait(); p.Close() })
	p.WaitWorkers()
	view := target.GetView()
	if view.Sent != 1 || view.Recv != 1 {
		t.Fatalf("loopback stats: sent=%d recv=%d loss=%d error=%q", view.Sent, view.Recv, view.Loss, view.LastError)
	}
}
