package main

import (
	"reflect"
	"testing"

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

func TestInitTargets(t *testing.T) {
	targets := initTargets([]targetSpec{{Host: "a"}, {Host: "b"}})
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].Host() != "a" || targets[1].Host() != "b" {
		t.Errorf("targets host mismatch")
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
			name:  "--resolve-all keeps names and DSCP of literal hosts",
			specs: []targetSpec{{Host: "127.0.0.1", Name: "loopback", DSCP: "EF"}},
			cfg:   config{resolveAll: true}, wantSame: true,
		},
		{
			name:   "--resolve-all keeps an IPv6 zone, DSCP and groups",
			specs:  []targetSpec{{Host: "fe80::1%en0", DSCP: "EF"}, {Host: "::1"}},
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
			got, gotGroups, err := expandTargets(tt.specs, tt.groups, tt.cfg)

			if err != nil {
				t.Fatalf("expandTargets: %v", err)
			}
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
