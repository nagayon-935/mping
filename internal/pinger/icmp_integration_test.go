//go:build integration

package pinger_test

// These tests send real ICMP echo requests over raw sockets, so they need
// root or CAP_NET_RAW and are built only with -tags integration. Every other
// pinger test substitutes the sockets; this is the one place the real
// kernel send/receive path, including the TTL control message, is
// exercised. CI runs it as:
//
//	go test -c -tags integration -o pinger-integration.test ./internal/pinger
//	sudo ./pinger-integration.test -test.run Integration -test.v

import (
	"net"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestIntegration_LoopbackEcho(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		network string
	}{
		{"IPv4", "127.0.0.1", "ip4:icmp"},
		{"IPv6", "::1", "ip6:ipv6-icmp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe, err := net.ListenPacket(tt.network, tt.host)
			if err != nil {
				if tt.network == "ip6:ipv6-icmp" {
					t.Skipf("no IPv6 loopback here: %v", err)
				}
				t.Fatalf("cannot open a raw ICMP socket (run as root or with CAP_NET_RAW): %v", err)
			}
			probe.Close()

			const count = 3
			target := stats.NewTargetStats(tt.host)
			p := pinger.NewPingerWithOptions([]*stats.TargetStats{target}, pinger.Options{})
			p.Count = count
			if err := p.Start(100*time.Millisecond, time.Second); err != nil {
				t.Fatalf("Start: %v", err)
			}
			p.WaitWorkers()
			p.Stop()
			p.Wait()
			p.Close()

			v := target.GetView()
			if v.Sent != count || v.Recv != count || v.Loss != 0 {
				t.Fatalf("sent/recv/loss = %d/%d/%d, want %d/%d/0 (last error %q)",
					v.Sent, v.Recv, v.Loss, count, count, v.LastError)
			}
			if v.LastRTT <= 0 {
				t.Errorf("LastRTT = %v, want > 0", v.LastRTT)
			}
			if v.LastTTL <= 0 {
				t.Errorf("LastTTL = %d, want > 0 from the TTL/hop-limit control message", v.LastTTL)
			}
			if v.IP != tt.host {
				t.Errorf("IP = %q, want %q", v.IP, tt.host)
			}
		})
	}
}
