//go:build integration

package main

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

// No raw ICMP or outgoing UDP payload is needed to exercise automatic
// source/family selection. Set MPING_TEST_HOST for an external hostname and
// MPING_TEST_INTERFACE to additionally verify an actual interface binding.
func TestIntegration_AutoAddressFamily(t *testing.T) {
	host := os.Getenv("MPING_TEST_HOST")
	if host == "" {
		host = "localhost"
	}
	cases := []config{{}}
	if name := os.Getenv("MPING_TEST_INTERFACE"); name != "" {
		cases = append(cases, config{ifaceName: name})
	}
	for _, cfg := range cases {
		name := cfg.ifaceName
		if name == "" {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			network := resolveNetwork(cfg)
			if network != "ip" {
				t.Fatalf("default family was fixed to %s", network)
			}
			opts := buildPingerOptions(cfg, network, nil, []targetSpec{{Host: host}})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			addr, err := opts.ResolveIPAddrContext(ctx, "ip", host)
			if err != nil || addr == nil || net.ParseIP(addr.IP.String()) == nil {
				t.Fatalf("selection=%v err=%v", addr, err)
			}
			bind, v4, v6, err := determineSourceIPs(cfg, []targetSpec{{Host: host}})
			if err != nil || bind != "" {
				t.Fatalf("automatic source was pinned: %q, err=%v", bind, err)
			}
			if addr.IP.To4() == nil && v6 == "" || addr.IP.To4() != nil && v4 == "" {
				t.Fatalf("selected family has no displayed source: %s, v4=%s v6=%s", addr, v4, v6)
			}
			t.Logf("%s -> %s (OS route/source selection, interface=%q, source4=%s, source6=%s)", host, addr, cfg.ifaceName, v4, v6)
		})
	}
}
