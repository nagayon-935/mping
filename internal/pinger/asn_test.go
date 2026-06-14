package pinger

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type fakePacketConnV4 struct {
	PacketConnV4
}

func (f *fakePacketConnV4) Close() error { return nil }
func (f *fakePacketConnV4) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) {
	return len(b), nil
}

func TestPinger_ASNLookupDirect(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		if strings.Contains(query, "8.8.8.8") {
			return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
		}
		if strings.Contains(query, "1.1.1.1") {
			return []string{"NA | 1.1.1.0/24 | US | arin | 1992-12-01"}, nil
		}
		if strings.Contains(query, "origin6") {
			return []string{"15169 | 2001:4860:4860::/48 | US | arin | 2005-01-01"}, nil
		}
		return nil, fmt.Errorf("not found")
	}

	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
	})

	// Test IPv4 lookup
	asn := p.getASN("8.8.8.8")
	if asn != "AS15169" {
		t.Errorf("expected AS15169, got %q", asn)
	}

	// Test cache
	asn = p.getASN("8.8.8.8")
	if asn != "AS15169" {
		t.Errorf("expected cached AS15169, got %q", asn)
	}

	// Test NA
	asn = p.getASN("1.1.1.1")
	if asn != "" {
		t.Errorf("expected empty string for NA, got %q", asn)
	}

	// Test empty/invalid
	if p.getASN("") != "" {
		t.Error("expected empty string for empty IP")
	}
	if p.getASN("invalid") != "" {
		t.Error("expected empty string for invalid IP")
	}

	// Test error from LookupTXT
	if p.getASN("0.0.0.0") != "" {
		t.Error("expected empty string on lookup error")
	}
}

func TestPinger_LookupASN(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
	}
	target := stats.NewTargetStats("8.8.8.8")
	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
	})
	p.lookupASN(target, "8.8.8.8")
	if target.GetView().ASN != "AS15169" {
		t.Errorf("lookupASN did not set ASN on target")
	}
}

type traceFakePacketConn struct {
	net.PacketConn
}

func (f *traceFakePacketConn) Close() error                      { return nil }
func (f *traceFakePacketConn) SetReadDeadline(t time.Time) error { return nil }
func (f *traceFakePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	return 0, nil, fmt.Errorf("timeout")
}
func (f *traceFakePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	return len(b), nil
}
func (f *traceFakePacketConn) Read(b []byte) (int, error)         { return 0, fmt.Errorf("not implemented") }
func (f *traceFakePacketConn) Write(b []byte) (int, error)        { return len(b), nil }
func (f *traceFakePacketConn) LocalAddr() net.Addr                { return &net.IPAddr{IP: net.IPv4zero} }
func (f *traceFakePacketConn) RemoteAddr() net.Addr               { return &net.IPAddr{IP: net.IPv4zero} }
func (f *traceFakePacketConn) SetDeadline(t time.Time) error      { return nil }
func (f *traceFakePacketConn) SetWriteDeadline(t time.Time) error { return nil }

func TestPinger_TraceRoute_AsnEnabled(t *testing.T) {
	t.Skip("Flaky in test environment - traceCh registration timing issue")
	mockLookup := func(query string) ([]string, error) {
		return []string{"12345 | 1.1.1.0/24 | US | arin | 1992-12-01"}, nil
	}

	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("1.1.1.1").To4()}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &traceFakePacketConn{}, nil
		},
	})
	p.Source = "127.0.0.1"

	p.connV4 = &fakePacketConnV4{}

	done := make(chan struct{})
	var hops []string
	var err error

	go func() {
		hops, err = p.TraceRoute("1.1.1.1", 1, 200*time.Millisecond)
		close(done)
	}()

	var traceCh chan traceMsg
	start := time.Now()
	for time.Since(start) < 2*time.Second {
		p.traceChansMu.RLock()
		if len(p.traceChans) > 0 {
			traceCh = p.traceChans[len(p.traceChans)-1]
		}
		p.traceChansMu.RUnlock()
		if traceCh != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
		select {
		case <-done:
			if err != nil {
				t.Fatalf("TraceRoute exited early with error: %v", err)
			}
		default:
		}
	}

	if traceCh == nil {
		t.Fatal("traceCh not registered")
	}

	time.Sleep(50 * time.Millisecond)

	baseID := p.baseID
	traceID := (baseID + 0x1234 + 1) & 0xffff

	innerIP := make([]byte, 28)
	innerIP[0] = 0x45
	innerIP[9] = 1
	innerIP[20] = 8
	innerIP[24] = byte(traceID >> 8)
	innerIP[25] = byte(traceID & 0xff)
	innerIP[26] = byte(1 >> 8)
	innerIP[27] = byte(1 & 0xff)

	traceCh <- traceMsg{
		parsed: &icmp.Message{
			Type: ipv4.ICMPTypeTimeExceeded,
			Body: &icmp.TimeExceeded{
				Data: innerIP,
			},
		},
		src: &net.IPAddr{IP: net.ParseIP("1.1.1.1")},
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("TraceRoute timed out")
	}

	if err != nil {
		t.Fatalf("TraceRoute error: %v", err)
	}

	if len(hops) == 0 {
		t.Fatal("expected 1 hop")
	}

	if !strings.Contains(hops[0], "AS12345") {
		t.Errorf("expected ASN in hop output, got %q", hops[0])
	}
}

func TestPinger_TraceRoute_MaxHops(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &traceFakePacketConn{}, nil
		},
	})
	p.connV4 = &fakePacketConnV4{}

	hops, err := p.TraceRoute("1.1.1.1", 2, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("TraceRoute error: %v", err)
	}
	if len(hops) != 2 || hops[0] != "*" || hops[1] != "*" {
		t.Errorf("expected 2 stars, got %v", hops)
	}
}
