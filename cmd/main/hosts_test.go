package main

import (
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/hostsfile"
	"github.com/nagayon-935/mping/internal/ui"
)

func TestTargetSpecDisplay(t *testing.T) {
	tests := []struct {
		name string
		spec targetSpec
		want string
	}{
		{name: "plain host", spec: targetSpec{Host: "gw.example.com"}, want: "gw.example.com"},
		{name: "pinned host", spec: targetSpec{Host: "gw.example.com", PinnedIP: "192.0.2.1"}, want: "gw.example.com (192.0.2.1)"},
		{name: "named host", spec: targetSpec{Host: "192.0.2.1", Name: "core-sw01"}, want: "core-sw01"},
		{name: "named pinned host", spec: targetSpec{Host: "gw.example.com", Name: "gw", PinnedIP: "192.0.2.1"}, want: "gw (192.0.2.1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.display(); got != tt.want {
				t.Fatalf("display() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildPingerOptions_ResolvesNamedTargetsToTheirHost(t *testing.T) {
	// The name must never resolve on its own (.invalid is reserved), so the
	// test can only pass through the display→address map.
	specs := []targetSpec{{Host: "127.0.0.1", Name: "named-target.invalid"}}
	opts := buildPingerOptions(config{}, "ip", nil, specs)

	addr, err := opts.ResolveIPAddr("ip", specs[0].display())

	if err != nil {
		t.Fatalf("resolve %q: %v", specs[0].display(), err)
	}
	if got := addr.IP.String(); got != "127.0.0.1" {
		t.Fatalf("resolved %q to %s, want 127.0.0.1", specs[0].display(), got)
	}
}

func TestBuildTargetsForIteration(t *testing.T) {
	targets := buildTargetsForIteration([]targetSpec{{Host: "a"}, {Host: "b"}}, config{})
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].Host() != "a" || targets[1].Host() != "b" {
		t.Errorf("targets host mismatch")
	}
}

func TestBuildTargetsForIterationDNSServerMatchesResolution(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{host: "example.com", want: "192.0.2.53"},
		{host: "192.0.2.1", want: "-"},
		{host: "2001:db8::1", want: "-"},
		// A zoned IPv6 literal is measured without DNS (netip.ParseAddr
		// accepts it), so it must not report the configured DNS server.
		{host: "fe80::1%en0", want: "-"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			targets := buildTargetsForIteration([]targetSpec{{Host: tt.host}}, config{dnsServer: "192.0.2.53"})

			if got := targets[0].GetView().DNSServer; got != tt.want {
				t.Errorf("DNSServer = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandTargets(t *testing.T) {
	groups := []ui.TargetGroup{{Name: "G1", Indices: []int{0}}}
	tests := []struct {
		name   string
		specs  []targetSpec
		groups []ui.TargetGroup
		cfg    config

		// wantSame: the specs (and groups) must come back untouched.
		wantSame bool
		// wantDisplay0, when set, is the display name of the first result.
		wantDisplay0 string
		// wantAtLeast is the minimum number of expanded targets.
		wantAtLeast int
	}{
		{
			name:  "without --resolve-all hosts are returned as given",
			specs: []targetSpec{{Host: "example.com"}, {Host: "8.8.8.8"}}, groups: groups,
			wantSame: true,
		},
		{
			name:  "--resolve-all keeps an already pinned host",
			specs: []targetSpec{{Host: "example.com", PinnedIP: "1.1.1.1"}, {Host: "8.8.8.8"}}, groups: groups,
			cfg: config{resolveAll: true}, wantSame: true, wantDisplay0: "example.com (1.1.1.1)",
		},
		{
			name:  "--resolve-all keeps names of literal hosts",
			specs: []targetSpec{{Host: "127.0.0.1", Name: "loopback"}},
			cfg:   config{resolveAll: true}, wantSame: true,
		},
		{
			name:   "--resolve-all keeps an IPv6 zone and groups",
			specs:  []targetSpec{{Host: "fe80::1%en0"}, {Host: "::1"}},
			groups: []ui.TargetGroup{{Name: "local", Indices: []int{0, 1}}},
			cfg:    config{resolveAll: true, ipv6Only: true, sourceAddr: "::1"}, wantSame: true,
		},
		{
			name:  "--resolve-all expands a resolvable host",
			specs: []targetSpec{{Host: "localhost"}}, groups: groups,
			cfg: config{resolveAll: true}, wantAtLeast: 1,
		},
		{
			name:  "--resolve-all leaves an unresolvable host as is",
			specs: []targetSpec{{Host: "invalid.invalid"}}, groups: groups,
			cfg: config{resolveAll: true}, wantSame: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotGroups := expandTargets(tt.specs, tt.groups, tt.cfg)

			if tt.wantSame && (!reflect.DeepEqual(got, tt.specs) || !reflect.DeepEqual(gotGroups, tt.groups)) {
				t.Errorf("specs/groups changed: got %+v %+v, want %+v %+v", got, gotGroups, tt.specs, tt.groups)
			}
			if len(got) < tt.wantAtLeast {
				t.Errorf("expected at least %d targets, got %v", tt.wantAtLeast, got)
			}
			if tt.wantDisplay0 != "" && got[0].display() != tt.wantDisplay0 {
				t.Errorf("first target = %q, want %q", got[0].display(), tt.wantDisplay0)
			}
		})
	}
}

func TestExpandHostAddressesReusesDNSAcrossGroups(t *testing.T) {
	specs := []targetSpec{
		{Host: "router", Name: "first"}, {Host: "missing"},
		{Host: "router", Name: "second"}, {Host: "router", PinnedIP: "192.0.2.9"},
		{Host: "fe80::1%en0"}, {Host: "missing", Name: "retry"},
	}
	groups := []ui.TargetGroup{{Name: "routers", Indices: []int{2, 0, 3}}, {Name: "other", Indices: []int{4, 1, 5}}}
	calls := map[string]int{}
	got, gotGroups := expandHostAddresses(specs, groups, func(host string) []net.IP {
		calls[host]++
		if host == "router" {
			return []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}
		}
		return nil
	})
	want := []targetSpec{
		{Host: "router", Name: "first", PinnedIP: "192.0.2.1"},
		{Host: "router", Name: "first", PinnedIP: "2001:db8::1"}, specs[1],
		{Host: "router", Name: "second", PinnedIP: "192.0.2.1"},
		{Host: "router", Name: "second", PinnedIP: "2001:db8::1"}, specs[3], specs[4], specs[5],
	}
	wantGroups := []ui.TargetGroup{{Name: "routers", Indices: []int{3, 4, 0, 1, 5}}, {Name: "other", Indices: []int{6, 2, 7}}}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("expanded hosts = %+v, groups = %+v", got, gotGroups)
	}
	if !reflect.DeepEqual(calls, map[string]int{"router": 1, "missing": 1}) {
		t.Fatalf("lookup calls = %v", calls)
	}
}

func TestParseHostsFile_Include(t *testing.T) {
	dir := t.TempDir()
	const header = "IP Address,Status,DNS Name\n"
	writeTestFile(t, dir, "top.csv", header+"192.0.2.1/24,Active,\n")
	writeTestFile(t, dir, "branch-a.csv", header+"198.51.100.1/24,Active,branch-a\n")
	writeTestFile(t, dir, "branch-b.csv", header+"198.51.100.2/24,Active,branch-b\n")
	path := writeTestFile(t, dir, "hosts.yaml", `hosts:
  - 8.8.8.8
include: top.csv
groups:
  - name: Branches
    include: [branch-a.csv, branch-b.csv]
  - name: Mixed
    hosts: [1.1.1.1]
    include: top.csv
`)

	doc, err := hostsfile.Parse(path)

	if err != nil {
		t.Fatalf("hostsfile.Parse: %v", err)
	}
	if want := []hostsfile.Entry{{Host: "8.8.8.8"}, {Host: "192.0.2.1"}}; !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("hosts = %v, want %v (inline entries first, then includes)", doc.Hosts, want)
	}
	wantGroups := []hostsfile.Group{
		{Name: "Branches", Hosts: []hostsfile.Entry{{Host: "198.51.100.1", Name: "branch-a"}, {Host: "198.51.100.2", Name: "branch-b"}}},
		{Name: "Mixed", Hosts: []hostsfile.Entry{{Host: "1.1.1.1"}, {Host: "192.0.2.1"}}},
	}
	if !reflect.DeepEqual(doc.Groups, wantGroups) {
		t.Fatalf("groups = %#v, want %#v", doc.Groups, wantGroups)
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Fatalf("an include-only group must pass validation: %v", err)
	}
}

func TestValidateHostsDoc_Names(t *testing.T) {
	tests := []struct {
		name    string
		doc     hostsfile.Document
		wantErr string
	}{
		{
			name: "distinct names are valid",
			doc: hostsfile.Document{
				Hosts:  []hostsfile.Entry{{Host: "10.0.0.1", Name: "a"}, {Host: "10.0.0.1", Name: "b"}},
				Groups: []hostsfile.Group{{Name: "G", Hosts: []hostsfile.Entry{{Host: "10.0.0.2", Name: "c"}}}},
			},
		},
		{
			name: "duplicate name across groups",
			doc: hostsfile.Document{
				Hosts:  []hostsfile.Entry{{Host: "10.0.0.1", Name: "core"}},
				Groups: []hostsfile.Group{{Name: "G", Hosts: []hostsfile.Entry{{Host: "10.0.0.2", Name: "core"}}}},
			},
			wantErr: `duplicate name "core"`,
		},
		{
			name: "name equal to another entry's host",
			doc: hostsfile.Document{
				Hosts: []hostsfile.Entry{{Host: "10.0.0.1", Name: "10.0.0.2"}, {Host: "10.0.0.2"}},
			},
			wantErr: `name "10.0.0.2" is also used as a host`,
		},
		{
			name: "name equal to its own host is allowed",
			doc: hostsfile.Document{
				Hosts: []hostsfile.Entry{{Host: "gw", Name: "gw"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHostsDoc(tt.doc)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestBuildHostsAndGroups_CarriesNames(t *testing.T) {
	specs, _ := buildHostsAndGroups(
		[]hostsfile.Entry{{Host: "10.0.0.1", Name: "core"}},
		[]hostsfile.Group{{Name: "G", Hosts: []hostsfile.Entry{{Host: "10.0.0.2", Name: "edge"}}}},
		[]string{"cli.example"},
	)

	want := []targetSpec{
		{Host: "10.0.0.1", Name: "core"},
		{Host: "cli.example"},
		{Host: "10.0.0.2", Name: "edge"},
	}
	if !reflect.DeepEqual(specs, want) {
		t.Fatalf("specs = %#v, want %#v", specs, want)
	}
}
