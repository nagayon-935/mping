package main

import (
	"errors"
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
			name:      "only loopback addresses -> error",
			addrCIDRs: []string{"127.0.0.1/8"},
			ifaceName: "lo",
			wantIPv6:  false,
			wantErr:   true,
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

func TestGetInterfaceIP_LookupFailure(t *testing.T) {
	lookupErr := errors.New("not found")
	stubInterfaceByName(t, func(string) (*net.Interface, error) { return nil, lookupErr })

	_, err := getInterfaceIP("nonexistent0", true)

	if !errors.Is(err, lookupErr) {
		t.Errorf("getInterfaceIP error = %v, want it to wrap the lookup error", err)
	}
}

// stubInterfaceByName swaps the interface lookup seam for the test.
func stubInterfaceByName(t *testing.T, fn func(string) (*net.Interface, error)) {
	t.Helper()
	orig := interfaceByName
	interfaceByName = fn
	t.Cleanup(func() { interfaceByName = orig })
}

// stubOutboundIP swaps the outbound-address probe seam for the test.
func stubOutboundIP(t *testing.T, fn func(remote, network string) string) {
	t.Helper()
	orig := getPreferredOutboundIPFn
	getPreferredOutboundIPFn = fn
	t.Cleanup(func() { getPreferredOutboundIPFn = orig })
}

func TestGetInterfaceMTU(t *testing.T) {
	tests := []struct {
		name       string
		ifaceName  string
		sourceIP   string
		lookup     func(string) (*net.Interface, error)
		interfaces func() ([]net.Interface, error)
		wantMTU    int
		wantErr    bool
	}{
		{
			name:      "named interface reports its MTU",
			ifaceName: "eth0",
			lookup: func(name string) (*net.Interface, error) {
				if name == "eth0" {
					return &net.Interface{MTU: 1400}, nil
				}
				return nil, errors.New("not found")
			},
			wantMTU: 1400,
		},
		{
			name:      "unknown interface is an error",
			ifaceName: "no-such-iface",
			lookup:    func(string) (*net.Interface, error) { return nil, errors.New("not found") },
			wantErr:   true,
		},
		{
			name:       "source IP: interface listing failure is an error",
			sourceIP:   "192.0.2.1",
			interfaces: func() ([]net.Interface, error) { return nil, errors.New("list failed") },
			wantErr:    true,
		},
		{
			name:     "source IP owned by no interface is an error",
			sourceIP: "192.0.2.1",
			interfaces: func() ([]net.Interface, error) {
				return []net.Interface{{Name: "eth1", MTU: 1300}}, nil
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.lookup != nil {
				stubInterfaceByName(t, tt.lookup)
			}
			if tt.interfaces != nil {
				orig := netInterfaces
				netInterfaces = tt.interfaces
				t.Cleanup(func() { netInterfaces = orig })
			}

			mtu, err := getInterfaceMTU(tt.ifaceName, tt.sourceIP, "")

			if (err != nil) != tt.wantErr {
				t.Fatalf("getInterfaceMTU error = %v, wantErr %v", err, tt.wantErr)
			}
			if mtu != tt.wantMTU {
				t.Errorf("getInterfaceMTU = %d, want %d", mtu, tt.wantMTU)
			}
		})
	}
}

func TestGetPreferredOutboundIP_Localhost(t *testing.T) {
	ip := getPreferredOutboundIP("127.0.0.1", "udp4")
	if ip == "" {
		t.Skip("no outbound IP detected in this environment")
	}
	if net.ParseIP(ip) == nil {
		t.Fatalf("expected valid IP, got %q", ip)
	}
}

func TestHasIPv6Connectivity(t *testing.T) {
	tests := []struct {
		name  string
		probe string
		want  bool
	}{
		{"no route", "", false},
		{"global address", "2001:db8::1", true},
		{"link-local address is not connectivity", "fe80::1", false},
		{"loopback is not connectivity", "::1", false},
		{"unparsable address", "invalid", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubOutboundIP(t, func(string, string) string { return tt.probe })

			if got := hasIPv6Connectivity(); got != tt.want {
				t.Fatalf("hasIPv6Connectivity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveNetwork(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config
		probe string
		want  string
	}{
		{"-4 forces ip4", config{ipv4Only: true}, "2001:db8::1", "ip4"},
		{"-6 forces ip6", config{ipv6Only: true}, "", "ip6"},
		{"dual stack when IPv6 is reachable", config{}, "2001:db8::1", "ip"},
		{"falls back to ip4 without IPv6", config{}, "", "ip4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubOutboundIP(t, func(string, string) string { return tt.probe })

			if got := resolveNetwork(tt.cfg); got != tt.want {
				t.Fatalf("resolveNetwork() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectAutoSourceIPs(t *testing.T) {
	routes := func(remote, network string) string {
		switch network {
		case "udp4":
			return "10.0.0.2"
		case "udp6":
			return "2001:db8::2"
		}
		return ""
	}
	noRoute := func(string, string) string { return "" }
	tests := []struct {
		name      string
		specs     []targetSpec
		probe     func(remote, network string) string
		want4     string
		want6     string
		wantProbe bool // whether the probe is expected to be consulted at all
	}{
		{"IPv4 target yields only a v4 source", []targetSpec{{Host: "192.0.2.1"}}, routes, "10.0.0.2", "", true},
		{"IPv6 target yields only a v6 source", []targetSpec{{Host: "2001:db8::1"}}, routes, "", "2001:db8::2", true},
		{"pinned IP is used instead of the hostname", []targetSpec{{Host: "localhost", PinnedIP: "192.0.2.1"}}, routes, "10.0.0.2", "", true},
		{"both families found across targets", []targetSpec{{Host: "192.0.2.1"}, {Host: "2001:db8::1"}}, routes, "10.0.0.2", "2001:db8::2", true},
		{"no route leaves both empty", []targetSpec{{Host: "192.0.2.1"}, {Host: "2001:db8::1"}}, noRoute, "", "", true},
		{"unresolvable host never reaches the probe", []targetSpec{{Host: "invalid.invalid"}}, routes, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probed := false
			stubOutboundIP(t, func(remote, network string) string {
				probed = true
				return tt.probe(remote, network)
			})

			got4, got6 := detectAutoSourceIPs(tt.specs)

			if got4 != tt.want4 || got6 != tt.want6 {
				t.Errorf("detectAutoSourceIPs() = (%q, %q), want (%q, %q)", got4, got6, tt.want4, tt.want6)
			}
			if probed != tt.wantProbe {
				t.Errorf("probe consulted = %v, want %v", probed, tt.wantProbe)
			}
		})
	}
}
