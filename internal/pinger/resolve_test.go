package pinger

import (
	"context"
	"errors"
	"github.com/nagayon-935/mping/internal/stats"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeDNS answers A queries with 192.0.2.10 and AAAA queries with 2001:db8::10,
// except that AAAA queries are silently dropped when dropAAAA is set. It
// records every query type it receives.
type fakeDNS struct {
	conn     net.PacketConn
	dropAAAA bool
	a, aaaa  net.IP

	mu    sync.Mutex
	types []dnsmessage.Type
}

func startFakeDNS(t *testing.T, dropAAAA bool) *fakeDNS {
	return startFakeDNSAddresses(t, dropAAAA, "192.0.2.10", "2001:db8::10")
}

func startFakeDNSAddresses(t *testing.T, dropAAAA bool, a, aaaa string) *fakeDNS {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeDNS{conn: conn, dropAAAA: dropAAAA, a: net.ParseIP(a), aaaa: net.ParseIP(aaaa)}
	t.Cleanup(func() { conn.Close() })
	go f.serve()
	return f
}

func (f *fakeDNS) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := f.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var msg dnsmessage.Message
		if err := msg.Unpack(buf[:n]); err != nil || len(msg.Questions) != 1 {
			continue
		}
		q := msg.Questions[0]
		f.mu.Lock()
		f.types = append(f.types, q.Type)
		f.mu.Unlock()
		if q.Type == dnsmessage.TypeAAAA && f.dropAAAA {
			continue
		}
		resp := dnsmessage.Message{
			Header:    dnsmessage.Header{ID: msg.ID, Response: true, Authoritative: true, RecursionDesired: msg.RecursionDesired},
			Questions: msg.Questions,
		}
		hdr := dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 60}
		switch q.Type {
		case dnsmessage.TypeA:
			var a [4]byte
			copy(a[:], f.a.To4())
			resp.Answers = []dnsmessage.Resource{{Header: hdr, Body: &dnsmessage.AResource{A: a}}}
		case dnsmessage.TypeAAAA:
			var aaaa [16]byte
			copy(aaaa[:], f.aaaa.To16())
			resp.Answers = []dnsmessage.Resource{{Header: hdr, Body: &dnsmessage.AAAAResource{
				AAAA: aaaa}}}
		}
		out, err := resp.Pack()
		if err != nil {
			continue
		}
		_, _ = f.conn.WriteTo(out, from)
	}
}

func (f *fakeDNS) queried(qt dnsmessage.Type) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.types, qt)
}

func (f *fakeDNS) resolver() *net.Resolver {
	addr := f.conn.LocalAddr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", addr)
		},
	}
}

func TestResolveIPAddrContext_IP4DoesNotWaitForAAAA(t *testing.T) {
	// Arrange: a DNS server that answers A immediately but never answers AAAA.
	dns := startFakeDNS(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Act
	addr, err := ResolveIPAddrContext(ctx, dns.resolver(), "ip4", "host.example.test.")

	// Assert
	if err != nil {
		t.Fatalf("ip4 lookup failed while only AAAA was unanswered: %v", err)
	}
	if got := addr.IP.String(); got != "192.0.2.10" {
		t.Errorf("addr = %s, want 192.0.2.10", got)
	}
	if dns.queried(dnsmessage.TypeAAAA) {
		t.Error("ip4 lookup sent an AAAA query; want A only")
	}
}

func TestResolveIPAddrContext_IP6QueriesOnlyAAAA(t *testing.T) {
	dns := startFakeDNS(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	addr, err := ResolveIPAddrContext(ctx, dns.resolver(), "ip6", "host.example.test.")

	if err != nil {
		t.Fatalf("ip6 lookup: %v", err)
	}
	if got := addr.IP.String(); got != "2001:db8::10" {
		t.Errorf("addr = %s, want 2001:db8::10", got)
	}
	if dns.queried(dnsmessage.TypeA) {
		t.Error("ip6 lookup sent an A query; want AAAA only")
	}
}

func TestPickIPAddrPreservesOSOrder(t *testing.T) {
	for _, first := range []string{"2001:db8::10", "192.0.2.10"} {
		other := "192.0.2.10"
		if first == other {
			other = "2001:db8::10"
		}
		addr, err := pickIPAddr("ip", "host.example.test", []net.IPAddr{{IP: net.ParseIP(first)}, {IP: net.ParseIP(other)}})
		if err != nil || addr.IP.String() != first {
			t.Fatalf("OS preference %s overridden: %v %v", first, addr, err)
		}
	}
}

func TestResolveUsableIPAddrContextHonorsSourceFamily(t *testing.T) {
	dns := startFakeDNSAddresses(t, false, "127.0.0.1", "::1")
	for _, source := range []string{"127.0.0.1", "::1"} {
		t.Run(source, func(t *testing.T) {
			if source == "::1" {
				c, err := net.ListenPacket("udp6", "[::1]:0")
				if err != nil {
					t.Skipf("IPv6 unavailable: %v", err)
				}
				c.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			addr, err := ResolveUsableIPAddrContext(ctx, dns.resolver(), "ip", "host.example.test.", BindConfig{Source: source})
			if err != nil || addr.IP.String() != source {
				t.Fatalf("source=%s addr=%v err=%v", source, addr, err)
			}
		})
	}
}

func TestResolveUsableIPAddrContextRejectsUnassignableSource(t *testing.T) {
	dns := startFakeDNSAddresses(t, false, "127.0.0.1", "::1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	addr, err := ResolveUsableIPAddrContext(ctx, dns.resolver(), "ip", "host.example.test.", BindConfig{Source: "2001:db8::dead"})
	if err == nil {
		t.Fatalf("unassignable source was ignored: addr=%v", addr)
	}
}

func TestResolveUsableIPAddrContextForcedFamilyAndCancellation(t *testing.T) {
	dns := startFakeDNSAddresses(t, true, "127.0.0.1", "::1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	addr, err := ResolveUsableIPAddrContext(ctx, dns.resolver(), "ip4", "host.example.test.", BindConfig{})
	if err != nil || addr.IP.String() != "127.0.0.1" || dns.queried(dnsmessage.TypeAAAA) {
		t.Fatalf("forced IPv4: addr=%v err=%v", addr, err)
	}
	cancel()
	if _, err := ResolveUsableIPAddrContext(ctx, dns.resolver(), "ip", "other.example.test.", BindConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lookup: %v", err)
	}
	addr, err = ResolveUsableIPAddrContext(context.Background(), nil, "ip", "fe80::1%test0", BindConfig{})
	if err != nil || addr.Zone != "test0" {
		t.Fatalf("literal zone lost: %v %v", addr, err)
	}
}

func TestResolveIPAddrContext_Literals(t *testing.T) {
	tests := []struct {
		name     string
		network  string
		address  string
		wantIP   string
		wantZone string
		wantErr  bool
	}{
		{name: "ipv4 literal on ip4", network: "ip4", address: "198.51.100.1", wantIP: "198.51.100.1"},
		{name: "ipv6 literal with zone on ip6", network: "ip6", address: "fe80::1%lo0", wantIP: "fe80::1", wantZone: "lo0"},
		{name: "ipv6 literal with zone on ip", network: "ip", address: "fe80::1%lo0", wantIP: "fe80::1", wantZone: "lo0"},
		{name: "ipv6 literal on ip4 is rejected", network: "ip4", address: "2001:db8::1", wantErr: true},
		{name: "ipv4 literal on ip6 is rejected", network: "ip6", address: "198.51.100.1", wantErr: true},
		{name: "unknown network", network: "tcp", address: "198.51.100.1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := ResolveIPAddrContext(context.Background(), nil, tt.network, tt.address)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got %v, want error", addr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr.IP.String() != tt.wantIP || addr.Zone != tt.wantZone {
				t.Errorf("got %s%%%s, want %s%%%s", addr.IP, addr.Zone, tt.wantIP, tt.wantZone)
			}
		})
	}
}

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name      string
		resolve   func(network, address string) (*net.IPAddr, error)
		wantIP    string // empty means resolution must fail
		wantError string
	}{
		{"success records the address", resolveTo("1.2.3.4"), "1.2.3.4", ""},
		{"resolver error is a DNS Error", func(string, string) (*net.IPAddr, error) { return nil, errors.New("dns failure") }, "", "DNS Error"},
		{"resolver returning no address is a DNS Error", func(string, string) (*net.IPAddr, error) { return nil, nil }, "", "DNS Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: tt.resolve})

			addr := p.resolveTarget(target)

			view := target.GetView()
			if tt.wantIP == "" {
				if addr != nil || view.LastError != tt.wantError {
					t.Fatalf("addr=%v LastError=%q, want nil and %q", addr, view.LastError, tt.wantError)
				}
				return
			}
			if addr == nil || addr.IP.String() != tt.wantIP || view.IP != tt.wantIP {
				t.Fatalf("addr=%v target IP=%q, want %s", addr, view.IP, tt.wantIP)
			}
		})
	}
}

func TestResolveIPAddrContext_UnknownNetworkAndDefaultResolver(t *testing.T) {
	if _, err := ResolveIPAddrContext(context.Background(), nil, "tcp", "127.0.0.1"); err == nil {
		t.Error("expected an error for an unsupported network")
	}
	// A nil resolver falls back to net.DefaultResolver; a cancelled context
	// makes that lookup fail immediately, without touching the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveIPAddrContext(ctx, nil, "ip4", "host.invalid"); err == nil {
		t.Error("expected the cancelled lookup to fail")
	}
}

func TestResolveIPAddrContext_PingerCancelled(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
		t.Error("lookup started on a cancelled context")
		return nil, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.resolveIPAddrContext(ctx, "ip", "example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
