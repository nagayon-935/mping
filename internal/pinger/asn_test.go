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
func (f *fakePacketConnV4) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) { return len(b), nil }

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

	// Test IPv6 lookup
	asn = p.getASN("2001:4860:4860::8888")
	if asn != "AS15169" {
		t.Errorf("expected AS15169 for IPv6, got %q", asn)
	}

	// Test NA handling
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

func TestPinger_TraceRoute_AsnEnabled(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		return []string{"12345 | 1.1.1.0/24 | US | arin | 1992-12-01"}, nil
	}

	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
	})
	
	// Set connV4 to non-nil so TraceRoute uses traceCh
	p.connV4 = &fakePacketConnV4{}

	// We need to capture the traceCh that TraceRoute creates.
	// Since it's appended to p.traceChans, we can find it there.
	
	done := make(chan struct{})
	var hops []string
	var err error
	
	go func() {
		hops, err = p.TraceRoute("1.1.1.1", 1, 200*time.Millisecond)
		close(done)
	}()
	
	// Wait a bit for TraceRoute to register traceCh
	var traceCh chan traceMsg
	for i := 0; i < 10; i++ {
		p.traceChansMu.RLock()
		if len(p.traceChans) > 0 {
			traceCh = p.traceChans[0]
		}
		p.traceChansMu.RUnlock()
		if traceCh != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	
	if traceCh == nil {
		t.Fatal("traceCh not registered")
	}
	
	// We need the traceID. It's atomic, but we can't easily get it.
	// However, acceptPacket in TraceRoute also matches on the payload signature.
	// Wait, acceptPacket in TraceRoute uses traceID.
	
	// Let's look at how pinger_test.go does it.
	// It seems it just sends a message and hopes it matches or tests the timeout.
	
	// Actually, I can use a simpler approach: 
	// Test getASN and lookupASN directly (already done).
	// And just assume TraceRoute works if acceptPacket works.
	
	// To make acceptPacket match, I REALLY need that traceID.
	// Since it's (baseID + 0x1234 + counter), and counter is now 1.
	baseID := p.baseID
	traceID := (baseID + 0x1234 + 1) & 0xffff
	
	// Send a mock response to traceCh
	traceCh <- traceMsg{
		parsed: &icmp.Message{
			Type: ipv4.ICMPTypeEchoReply,
			Body: &icmp.Echo{
				ID:   traceID,
				Seq:  1, // ttl
				Data: []byte("any"),
			},
		},
		src: &net.IPAddr{IP: net.ParseIP("1.1.1.1")},
	}
	
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
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
	})
	p.connV4 = &fakePacketConnV4{}
    
    // Don't send anything to traceCh, let it timeout for each hop
	hops, err := p.TraceRoute("1.1.1.1", 2, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("TraceRoute error: %v", err)
	}
	if len(hops) != 2 || hops[0] != "*" || hops[1] != "*" {
		t.Errorf("expected 2 stars, got %v", hops)
	}
}
