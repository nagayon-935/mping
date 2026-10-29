package pinger

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// resolveIPAddrBounded applies resolveTimeout and aborts when Stop closes
// p.done, so a hung resolver cannot stall workers or Pinger.Wait.
func (p *Pinger) resolveIPAddrBounded(network, address string) (*net.IPAddr, error) {
	return p.resolveIPAddrContext(context.Background(), network, address)
}

// resolveTarget attempts DNS resolution and updates the target's IP.
// Returns the resolved address, or nil if resolution failed or the pinger
// was stopped mid-resolution.
func (p *Pinger) resolveTarget(t *stats.TargetStats) *net.IPAddr {
	return p.resolveTargetContext(context.Background(), t)
}

func (p *Pinger) getASN(ipStr string) string {
	return p.getASNInfo(ipStr).Number
}

// sendProbe marshals and sends an ICMP echo request. Returns the send time, or an error string.
func (p *Pinger) sendProbe(t *stats.TargetStats, id, seq int, payload []byte, dstAddr *net.IPAddr) (time.Time, bool) {
	return p.sendProbeWithStats(t, t.NewProbe(), id, seq, payload, dstAddr)
}

// runWorker is a test helper that runs runTargetWorker (the production worker
// loop; see its doc) with a background context.
func (p *Pinger) runWorker(t *stats.TargetStats, id int, interval, timeout time.Duration) {
	p.runTargetWorker(t, id, interval, timeout, context.Background())
}

func (pc *PortChecker) check(t *stats.TargetStats, spec PortSpec, result *stats.PortCheckResult) {
	pc.checkContext(pc.ctx, t, spec, result)
}

func (p *Pinger) OpenHopSocket(dest string) (*HopSocket, error) {
	return p.OpenHopSocketContext(context.Background(), dest)
}

// resolveTo returns an Options.ResolveIPAddr hook that resolves every host to ip.
func resolveTo(ip string) func(network, address string) (*net.IPAddr, error) {
	return func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.ParseIP(ip)}, nil
	}
}

// listenWith returns an Options.ListenPacket hook that always hands out conn.
func listenWith(conn net.PacketConn) func(network, address string) (net.PacketConn, error) {
	return func(network, address string) (net.PacketConn, error) { return conn, nil }
}

// ipPacketWithEcho returns what an ICMP error message carries back: the
// original IP header followed by the echo request that triggered the error.
func ipPacketWithEcho(v6 bool, id, seq int) []byte {
	var echo icmp.Message
	hdr := make([]byte, 20)
	hdr[0], hdr[9] = 0x45, 1 // IPv4, IHL=5, protocol ICMP
	echo.Type = ipv4.ICMPTypeEcho
	if v6 {
		hdr = make([]byte, 40)
		hdr[0], hdr[6] = 0x60, 58 // IPv6, next header ICMPv6
		echo.Type = ipv6.ICMPTypeEchoRequest
	}
	echo.Body = &icmp.Echo{ID: id, Seq: seq, Data: []byte("x")}
	inner, err := echo.Marshal(nil)
	if err != nil {
		panic(err)
	}
	return append(hdr, inner...)
}

// eventually polls cond until it holds or timeout elapses, failing the test
// in the latter case. It replaces fixed sleeps that merely hope a goroutine
// has made progress.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v: %s", timeout, what)
		}
		time.Sleep(time.Millisecond)
	}
}

// startReceiver runs a receiver loop (runReceiverV4/V6) in the background.
// The returned stop func closes the pinger and fails the test unless the loop
// exits promptly; done is closed once the loop has returned.
func startReceiver(t *testing.T, p *Pinger, run func()) (stop func(), done <-chan struct{}) {
	t.Helper()
	d := make(chan struct{})
	go func() {
		run()
		close(d)
	}()
	return func() {
		t.Helper()
		p.Close()
		select {
		case <-d:
		case <-time.After(time.Second):
			t.Fatal("receiver did not stop")
		}
	}, d
}
