//go:build integration && (darwin || linux)

package pinger

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

// The kernel query and fragmentation options can be checked without root.
// MPING_TEST_IPV6_TARGET optionally selects a real external IPv6 destination.
// The connected UDP socket sends no UDP datagrams.
func TestIntegration_IPv6OSPathMTUQuery(t *testing.T) {
	target := os.Getenv("MPING_TEST_IPV6_TARGET")
	if target == "" {
		target = "::1"
	}
	addr, err := net.ResolveUDPAddr("udp6", net.JoinHostPort(target, "9"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp6", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	raw, err := c.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var optionErr error
	if err := raw.Control(func(fd uintptr) { optionErr = setIPv6PMTUDiscovery(int(fd)) }); err != nil {
		t.Fatal(err)
	}
	if optionErr != nil {
		t.Fatal(optionErr)
	}
	mtu, err := ipv6SocketPathMTU(c)
	if err != nil || mtu < 1280 {
		t.Fatalf("kernel PMTU = %d, error = %v", mtu, err)
	}
	t.Logf("%s: OS PMTU estimate %d bytes", target, mtu)
}

func TestIntegration_IPv6PathMTU(t *testing.T) {
	target := os.Getenv("MPING_TEST_IPV6_TARGET")
	if target == "" {
		target = "::1"
	}
	p := NewPingerWithOptions(nil, Options{})
	defer p.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := p.DiscoverPathMTU(ctx, target, 9872, 56, func(s string) { t.Log(s) })
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("raw ICMP requires root/CAP_NET_RAW: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != "os" || result.MTU < 1280 || result.Payload != result.MTU-48 {
		t.Fatalf("invalid PMTU: %+v", result)
	}
	if target == "::1" && result.Status != "confirmed" {
		t.Fatalf("loopback MTU was not confirmed: %+v", result)
	}
	t.Logf("PMTU: %+v", result)
}
