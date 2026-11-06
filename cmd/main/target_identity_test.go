package main

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildPingerOptionsResolverSelection(t *testing.T) {
	for _, dnsServer := range []string{"", "192.0.2.53"} {
		for _, injected := range []bool{false, true} {
			name := "default"
			if injected {
				name = "injected"
			}
			if dnsServer != "" {
				name += "/dns-server"
			} else {
				name += "/no-dns-server"
			}
			t.Run(name, func(t *testing.T) {
				var defaultCalls, injectedCalls atomic.Int32
				// Fail in Dial so selection is observable without DNS traffic or
				// route-selection sockets. These tests must remain sequential:
				// nil resolution uses the package-wide default resolver.
				resolver := func(calls *atomic.Int32) *net.Resolver {
					return &net.Resolver{
						PreferGo: true,
						Dial: func(context.Context, string, string) (net.Conn, error) {
							calls.Add(1)
							return nil, errors.New("test DNS dial failure")
						},
					}
				}
				original := net.DefaultResolver
				net.DefaultResolver = resolver(&defaultCalls)
				t.Cleanup(func() { net.DefaultResolver = original })
				var customResolver *net.Resolver
				if injected {
					customResolver = resolver(&injectedCalls)
				}
				opts := buildPingerOptions(config{dnsServer: dnsServer}, "ip4", customResolver, nil)
				if opts.Resolver != customResolver {
					t.Fatal("ASN/PTR resolver changed")
				}
				lookups := []struct {
					name string
					fn   func(string, string) (*net.IPAddr, error)
				}{
					{"legacy", opts.ResolveIPAddr},
					{"context", func(network, address string) (*net.IPAddr, error) {
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						return opts.ResolveIPAddrContext(ctx, network, address)
					}},
				}
				for _, lookup := range lookups {
					t.Run(lookup.name, func(t *testing.T) {
						defaultCalls.Store(0)
						injectedCalls.Store(0)
						if _, err := lookup.fn("ip", "resolver-selection.example.invalid."); err == nil {
							t.Fatal("lookup succeeded despite failing DNS dial")
						}
						if injected {
							if injectedCalls.Load() == 0 || defaultCalls.Load() != 0 {
								t.Errorf("resolver calls: injected=%d default=%d, want injected only", injectedCalls.Load(), defaultCalls.Load())
							}
						} else if defaultCalls.Load() == 0 || injectedCalls.Load() != 0 {
							t.Errorf("resolver calls: injected=%d default=%d, want default only", injectedCalls.Load(), defaultCalls.Load())
						}
					})
				}
			})
		}
	}
}

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
