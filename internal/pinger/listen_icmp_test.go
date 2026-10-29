package pinger

import (
	"errors"
	"net"
	"testing"
)

func TestListenICMPNetworkAndBindAddress(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		v6          bool
		wantNetwork string
		wantAddr    string
	}{
		{"v4 wildcard", "", false, "ip4:icmp", "0.0.0.0"},
		{"v6 wildcard", "", true, "ip6:ipv6-icmp", "::"},
		{"v4 source", "192.0.2.1", false, "ip4:icmp", "192.0.2.1"},
		{"v6 source", "2001:db8::1", true, "ip6:ipv6-icmp", "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotNetwork, gotAddr string
			p := NewPingerWithOptions(nil, Options{
				ListenPacket: func(network, address string) (net.PacketConn, error) {
					gotNetwork, gotAddr = network, address
					return nil, errors.New("stop here")
				},
			})
			p.Source = tt.source

			_, err := p.listenICMP(tt.v6)

			if err == nil {
				t.Fatal("expected the listen error to propagate")
			}
			if gotNetwork != tt.wantNetwork || gotAddr != tt.wantAddr {
				t.Fatalf("listenPacket(%q, %q), want (%q, %q)", gotNetwork, gotAddr, tt.wantNetwork, tt.wantAddr)
			}
		})
	}
}
