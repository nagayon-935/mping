package main

import (
	"context"
	"net"
	"testing"
)

func TestResolverHonorsConfiguredFamily(t *testing.T) {
	for _, network := range []string{"ip4", "ip6"} {
		for _, pinned := range []bool{false, true} {
			for _, address := range []string{"127.0.0.1", "::1"} {
				specs := []targetSpec{{Host: address}}
				if pinned {
					specs[0] = targetSpec{Host: "example.invalid", PinnedIP: address}
				}
				opts := buildPingerOptions(config{dnsServer: "127.0.0.1"}, network, net.DefaultResolver, specs)
				got, err := opts.ResolveIPAddr("ip", specs[0].display())
				wantValid := (network == "ip4") == (net.ParseIP(address).To4() != nil)
				if (err == nil) != wantValid {
					t.Fatalf("%s address=%s pinned=%v got=%v err=%v", network, address, pinned, got, err)
				}
			}
		}
	}
	opts := buildPingerOptions(config{}, "ip6", nil, nil)
	addr, err := opts.ResolveIPAddr("ip", "fe80::1%test0")
	if err != nil || addr.Zone != "test0" {
		t.Fatalf("IPv6 zone lost: %v %v", addr, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := opts.ResolveIPAddrContext(ctx, "ip", "cancelled.invalid"); err == nil {
		t.Fatal("canceled lookup succeeded")
	}
}
