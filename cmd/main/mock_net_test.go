package main

import (
	"fmt"
	"net"
	"testing"
)

// ipNetAddr builds a *net.IPNet as returned by (*net.Interface).Addrs() for
// the given CIDR string, e.g. "fe80::1/64" or "192.168.1.10/24".
func ipNetAddr(t *testing.T, cidr string) net.Addr {
	t.Helper()
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", cidr, err)
	}
	return &net.IPNet{IP: ip, Mask: ipnet.Mask}
}

func TestSelectInterfaceIP(t *testing.T) {
	tests := []struct {
		name      string
		addrCIDRs []string
		ifaceName string
		wantIPv6  bool
		wantIP    string
		wantErr   bool
	}{
		{
			name:      "IPv6 link-local first, global unicast present -> global wins",
			addrCIDRs: []string{"fe80::15:1516:a471:9b86/64", "2001:db8::1/64"},
			ifaceName: "en0",
			wantIPv6:  true,
			wantIP:    "2001:db8::1",
		},
		{
			name:      "IPv6 link-local only -> zone appended",
			addrCIDRs: []string{"fe80::15:1516:a471:9b86/64"},
			ifaceName: "en0",
			wantIPv6:  true,
			wantIP:    "fe80::15:1516:a471:9b86%en0",
		},
		{
			name:      "IPv6 ULA beats link-local",
			addrCIDRs: []string{"fe80::1/64", "fd00::1/8"},
			ifaceName: "en0",
			wantIPv6:  true,
			wantIP:    "fd00::1",
		},
		{
			name:      "IPv6 global unicast beats ULA",
			addrCIDRs: []string{"fd00::1/8", "2001:db8::1/64"},
			ifaceName: "en0",
			wantIPv6:  true,
			wantIP:    "2001:db8::1",
		},
		{
			name:      "IPv6 loopback ignored",
			addrCIDRs: []string{"::1/128", "fe80::1/64"},
			ifaceName: "en0",
			wantIPv6:  true,
			wantIP:    "fe80::1%en0",
		},
		{
			name:      "IPv4 existing behavior: first non-loopback address wins",
			addrCIDRs: []string{"127.0.0.1/8", "192.168.1.10/24", "10.0.0.5/24"},
			ifaceName: "eth0",
			wantIPv6:  false,
			wantIP:    "192.168.1.10",
		},
		{
			name:      "no IPv6 address on interface -> error",
			addrCIDRs: []string{"192.168.1.10/24"},
			ifaceName: "eth0",
			wantIPv6:  true,
			wantErr:   true,
		},
		{
			name:      "no IPv4 address on interface -> error",
			addrCIDRs: []string{"2001:db8::1/64"},
			ifaceName: "eth0",
			wantIPv6:  false,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addrs := make([]net.Addr, 0, len(tt.addrCIDRs))
			for _, cidr := range tt.addrCIDRs {
				addrs = append(addrs, ipNetAddr(t, cidr))
			}

			got, err := selectInterfaceIP(addrs, tt.ifaceName, tt.wantIPv6)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got IP %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantIP {
				t.Errorf("selectInterfaceIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestGetInterfaceIP(t *testing.T) {
	oldInterfaceByName := interfaceByName
	defer func() { interfaceByName = oldInterfaceByName }()

	interfaceByName = func(name string) (*net.Interface, error) {
		return nil, fmt.Errorf("not found")
	}

	if _, err := getInterfaceIP("nonexistent0", true); err == nil {
		t.Errorf("expected error when interface lookup fails")
	}
}

func TestGetInterfaceMTU(t *testing.T) {
	oldInterfaceByName := interfaceByName
	oldNetInterfaces := netInterfaces
	defer func() {
		interfaceByName = oldInterfaceByName
		netInterfaces = oldNetInterfaces
	}()

	interfaceByName = func(name string) (*net.Interface, error) {
		if name == "eth0" {
			return &net.Interface{MTU: 1400}, nil
		}
		return nil, fmt.Errorf("not found")
	}

	mtu, err := getInterfaceMTU("eth0", "", "")
	if err != nil || mtu != 1400 {
		t.Errorf("expected 1400, got %d (err: %v)", mtu, err)
	}

	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Name: "eth1", MTU: 1300},
		}, nil
	}
	// This will still fail to match Addrs() but at least we cover the loop.
}
