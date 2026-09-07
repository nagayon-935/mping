package pinger

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type immediateHopV4 struct {
	fakeHopConn
	p   *Pinger
	err error
}

func (f *immediateHopV4) WriteTo(b []byte, _ *ipv4.ControlMessage, dst net.Addr) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	msg, _ := icmp.ParseMessage(1, b)
	msg.Type = ipv4.ICMPTypeEchoReply
	f.p.broadcastTrace(msg, dst)
	return len(b), nil
}

type immediateHopV6 struct {
	fakeHopConnV6
	p *Pinger
}

func (f *immediateHopV6) WriteTo(b []byte, _ *ipv6.ControlMessage, dst net.Addr) (int, error) {
	msg, _ := icmp.ParseMessage(58, b)
	msg.Type = ipv6.ICMPTypeEchoReply
	f.p.broadcastTrace(msg, dst)
	return len(b), nil
}
func TestHopReplyDuringWriteIsDelivered(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		p := NewPinger(nil)
		sock := &HopSocket{isV4: !v6}
		dst := &net.IPAddr{IP: net.ParseIP("127.0.0.1")}
		if v6 {
			p.connV6 = &fakePacketConnV6{}
			sock.sendV6 = &immediateHopV6{p: p}
			dst.IP = net.ParseIP("::1")
		} else {
			p.connV4 = &fakePacketConn{}
			sock.sendV4 = &immediateHopV4{p: p}
		}
		reply, err := p.probeHopAddr(context.Background(), sock, dst, 1, 123, 50*time.Millisecond)
		if err != nil || !reply.Responded || !reply.ReachedDest {
			t.Fatalf("v6=%v fast response lost: %+v %v", v6, reply, err)
		}
		if len(p.traceChans) != 0 {
			t.Fatal("trace channel leaked")
		}
	}
}
func TestFailedHopSendUnregistersChannel(t *testing.T) {
	p := NewPinger(nil)
	p.connV4 = &fakePacketConn{}
	sock := &HopSocket{isV4: true, sendV4: &immediateHopV4{p: p, err: errors.New("send failed")}}
	if _, err := p.probeHopAddr(context.Background(), sock, &net.IPAddr{IP: net.ParseIP("127.0.0.1")}, 1, 10, time.Second); err == nil {
		t.Fatal("expected send error")
	}
	if len(p.traceChans) != 0 {
		t.Fatal("trace channel leaked after failed send")
	}
}

type pollingReceiver struct{ fakePacketConn }

func (f *pollingReceiver) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	time.Sleep(time.Millisecond)
	return 0, nil, nil, timeoutOpError()
}
func TestCountCompletionDoesNotWaitForReceiver(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ListenPacket: func(string, string) (net.PacketConn, error) { return nil, errors.New("use injected socket") },
	})
	p.connV4 = &pollingReceiver{}
	p.Count = 2
	if err := p.Start(5*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(); p.Wait(); p.Close() })
	workersDone := make(chan struct{})
	go func() { p.WaitWorkers(); close(workersDone) }()
	select {
	case <-workersDone:
	case <-time.After(time.Second):
		t.Fatal("count completion blocked on receiver")
	}
	v := target.GetView()
	if v.Sent != 2 || v.Loss != 2 {
		t.Fatalf("completion occurred before outstanding probes drained: sent=%d loss=%d", v.Sent, v.Loss)
	}
	allDone := make(chan struct{})
	go func() { p.Wait(); close(allDone) }()
	select {
	case <-allDone:
		t.Fatal("receiver stopped on worker completion")
	default:
	}
	p.Stop()
	select {
	case <-allDone:
	case <-time.After(time.Second):
		t.Fatal("receiver did not stop")
	}
}

func TestHopOperationsCancelDNS(t *testing.T) {
	for _, operation := range []string{"traceroute", "open", "probe", "pmtu"} {
		for _, legacy := range []bool{false, true} {
			t.Run(operation+map[bool]string{true: "/legacy", false: "/context"}[legacy], func(t *testing.T) {
				entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
				opts := Options{}
				if legacy {
					opts.ResolveIPAddr = func(string, string) (*net.IPAddr, error) {
						close(entered)
						defer close(exited)
						<-release
						return nil, context.Canceled
					}
				} else {
					opts.ResolveIPAddrContext = func(ctx context.Context, _, _ string) (*net.IPAddr, error) {
						close(entered)
						defer close(exited)
						<-ctx.Done()
						return nil, ctx.Err()
					}
				}
				p := NewPingerWithOptions(nil, opts)
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(func() { cancel(); close(release); <-exited })
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "traceroute":
						_, err = p.TraceRoute(ctx, "example.invalid", 1, time.Second)
					case "open":
						_, err = p.OpenHopSocketContext(ctx, "example.invalid")
					case "probe":
						_, err = p.ProbeHop(ctx, nil, "example.invalid", 1, 1, time.Second)
					case "pmtu":
						_, _, err = p.DiscoverMaxPayload(ctx, "example.invalid", 1500, 56, nil)
					}
					done <- err
				}()
				<-entered
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancellation: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("DNS ignored operation cancellation")
				}
			})
		}
	}
}

func TestSameHostDSCPUsesDifferentIPv6Packets(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("::1"), stats.NewTargetStats("::1"), stats.NewTargetStats("::1")}
	p := NewPingerWithOptions(targets, Options{TargetDSCP: map[int]int{0: 46 << 2, 1: 0}})
	conn := &fakePacketConnV6{}
	p.connV6 = conn
	for i, target := range targets {
		if _, ok := p.sendProbe(target, i+1, 1, []byte("MPING"), &net.IPAddr{IP: net.ParseIP("::1")}); !ok {
			t.Fatal("send failed")
		}
	}
	if len(conn.writeCMs) != 3 || conn.writeCMs[0] == nil || conn.writeCMs[0].TrafficClass != 46<<2 || conn.writeCMs[1] == nil || conn.writeCMs[1].TrafficClass != 0 || conn.writeCMs[2] != nil {
		t.Fatalf("same-host DSCP overrides mixed: %+v", conn.writeCMs)
	}
}
