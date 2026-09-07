package main

import (
	"context"
	"net"
	"reflect"
	"testing"

	"github.com/nagayon-935/mping/internal/pinger"
	ui "github.com/nagayon-935/mping/internal/ui"
)

func TestDuplicateHostDSCPRemainsIndependent(t *testing.T) {
	for _, pinned := range []string{"", "2001:db8::2"} {
		hosts := []targetSpec{{Host: "2001:db8::1", PinnedIP: pinned, DSCP: "EF"}, {Host: "2001:db8::1", PinnedIP: pinned, DSCP: "CS0"}, {Host: "2001:db8::1", PinnedIP: pinned}}
		targets := initTargets(hosts)
		opts := buildPingerOptions(config{dscp: "AF41"}, "ip6", nil, hosts)
		p := pinger.NewPingerWithOptions(targets, opts)
		for i, want := range []int{46 << 2, 0} {
			if got, ok := p.TargetDSCP[targets[i]]; !ok || got != want {
				t.Fatalf("target %d DSCP=(%d,%v) want %d", i, got, ok, want)
			}
		}
		if _, ok := p.TargetDSCP[targets[2]]; ok {
			t.Fatal("override leaked to same-host target using global default")
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

func TestHostEditsPreserveGroups(t *testing.T) {
	hosts := []targetSpec{{Host: "127.0.0.1"}, {Host: "127.0.0.2"}, {Host: "127.0.0.3"}}
	groups := []ui.TargetGroup{{Name: "group", Indices: []int{1, 2}}}
	for _, remove := range []string{"127.0.0.1", "127.0.0.2", "127.0.0.3"} {
		cfg, _, fs, _, err := parseArgs([]string{"-4", "127.0.0.1"})
		if err != nil {
			t.Fatal(err)
		}
		rc := newReloadCoordinator(fs, cfg, nil)
		opts := buildRunOptions(runOptionsParams{cfg: cfg, sup: &supervisor{}, rc: rc, sig: newReloadSignal(), currentHosts: hosts, currentGroups: groups})
		if err := opts.OnDeleteHost(remove); err != nil {
			t.Fatal(err)
		}
		updated, gs, _, _, _ := rc.apply(cfg, hosts, groups)
		var want, got []string
		for _, idx := range groups[0].Indices {
			if hosts[idx].Host != remove {
				want = append(want, hosts[idx].Host)
			}
		}
		for _, idx := range gs[0].Indices {
			if idx >= len(updated) {
				t.Fatalf("stale index: %v", gs)
			}
			got = append(got, updated[idx].Host)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("delete %s: members=%v want=%v", remove, got, want)
		}
	}
	duplicates := []targetSpec{{Host: "a"}, {Host: "same", DSCP: "EF"}, {Host: "same", DSCP: "CS0"}, {Host: "same", DSCP: "EF"}}
	gs := []ui.TargetGroup{{Name: "first", Indices: []int{1, 2}}, {Name: "second", Indices: []int{3}}}
	got := remapGroups(duplicates, duplicates[1:], gs)
	want := []ui.TargetGroup{{Name: "first", Indices: []int{0, 1}}, {Name: "second", Indices: []int{2}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicate memberships=%v want=%v", got, want)
	}
	added := append(append([]targetSpec(nil), hosts...), targetSpec{Host: "new"})
	if got := remapGroups(hosts, added, groups); !reflect.DeepEqual(got, groups) {
		t.Fatalf("add changed groups: %v", got)
	}
	if got := remapGroups(hosts, hosts[:1], groups); len(got) != 0 {
		t.Fatalf("empty group retained: %v", got)
	}
}
