package main

import (
	"context"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

type destinationTracer chan string

func (d destinationTracer) TraceRoute(_ context.Context, dest string, _ int, _ time.Duration) ([]string, error) {
	d <- dest
	return []string{dest}, nil
}

func TestRunTraceroutesUsesMonitoredAddress(t *testing.T) {
	for _, tc := range []struct{ name, ip, want string }{
		{"IPv4", "192.0.2.10", "192.0.2.10"},
		{"IPv6", "2001:db8::10", "2001:db8::10"},
		{"unresolved", "", "dns.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := stats.NewTargetStats("friendly label")
			target.SetResolveAddress("dns.example")
			target.SetIP(tc.ip)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := make(destinationTracer, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				runTraceroutes(ctx, called, target)
			}()
			select {
			case dest := <-called:
				if dest != tc.want {
					t.Errorf("traced %q; want monitored address %q", dest, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Error("traceroute did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("traceroute did not stop")
			}
		})
	}
}
