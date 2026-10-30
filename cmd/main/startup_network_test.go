package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/nagayon-935/mping/internal/pinger"
)

func TestDetermineSourceIPs(t *testing.T) {
	ifaceErr := errors.New("mock error")
	tests := []struct {
		name  string
		cfg   config
		hosts []targetSpec
		// stubs; nil leaves the real seam in place.
		iface func(string) (*net.Interface, error)
		probe func(remote, network string) string

		wantBind, wantV4, wantV6 string
		wantErr                  error
	}{
		{
			name:     "-S IPv4 address binds and reports as v4",
			cfg:      config{sourceAddr: "10.0.0.2"},
			wantBind: "10.0.0.2", wantV4: "10.0.0.2",
		},
		{
			name:     "-S IPv6 address binds and reports as v6",
			cfg:      config{sourceAddr: "2001:db8::1"},
			wantBind: "2001:db8::1", wantV6: "2001:db8::1",
		},
		{
			name:    "-I naming an unusable interface is an error",
			cfg:     config{ifaceName: "eth0"},
			iface:   func(string) (*net.Interface, error) { return nil, ifaceErr },
			wantErr: ifaceErr,
		},
		{
			name:   "without -S/-I the source is auto-detected for display only",
			hosts:  []targetSpec{{Host: "192.0.2.1"}},
			probe:  func(string, string) string { return "10.0.0.9" },
			wantV4: "10.0.0.9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.iface != nil {
				stubInterfaceByName(t, tt.iface)
			}
			if tt.probe != nil {
				stubOutboundIP(t, tt.probe)
			}

			bind, v4, v6, err := determineSourceIPs(tt.cfg, tt.hosts)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want it to wrap %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bind != tt.wantBind || v4 != tt.wantV4 || v6 != tt.wantV6 {
				t.Errorf("got bind=%q v4=%q v6=%q, want bind=%q v4=%q v6=%q", bind, v4, v6, tt.wantBind, tt.wantV4, tt.wantV6)
			}
		})
	}
}

func TestNewCustomResolver(t *testing.T) {
	t.Run("--dns-server builds a custom resolver", func(t *testing.T) {
		for _, server := range []string{"8.8.8.8", "8.8.8.8:53"} {
			r := newCustomResolver(server, pinger.BindConfig{})
			if r == net.DefaultResolver || !r.PreferGo || r.Dial == nil {
				t.Errorf("newCustomResolver(%q) = %+v, want a PreferGo resolver with a custom Dial", server, r)
			}
		}
	})

	// With no --dns-server the resolver must stay net.DefaultResolver
	// regardless of bind — bind
	// only takes effect once --dns-server picks a specific server to dial.
	t.Run("no --dns-server keeps the default resolver", func(t *testing.T) {
		r := newCustomResolver("", pinger.BindConfig{Source: "192.0.2.5", Interface: "eth0"})
		if r != net.DefaultResolver {
			t.Errorf("newCustomResolver(\"\", ...): got %p, want net.DefaultResolver (%p)", r, net.DefaultResolver)
		}
	})
}

// TestNewCustomResolver_DialHonoursBindConfig is the behavioral proof that
// -S actually reaches the resolver's dial: a real loopback UDP listener is
// reachable through the unbound resolver's Dial and stops being reachable
// once the resolver is told to source from an address the host does not
// own. This exercises exactly the func net.Resolver.Dial invokes internally
// during LookupIP, without needing a real DNS server to answer.
func TestNewCustomResolver_DialHonoursBindConfig(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()
	dnsServer := conn.LocalAddr().String()

	unbound := newCustomResolver(dnsServer, pinger.BindConfig{})
	if _, err := unbound.Dial(context.Background(), "udp", "ignored"); err != nil {
		t.Fatalf("unbound resolver Dial: unexpected error: %v", err)
	}

	bound := newCustomResolver(dnsServer, pinger.BindConfig{Source: "192.0.2.5"})
	if _, err := bound.Dial(context.Background(), "udp", "ignored"); err == nil {
		t.Error("resolver Dial with Source=192.0.2.5: got no error, want a dial failure — the source address was ignored")
	}
}
