package main

import (
	"reflect"
	"testing"
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

func TestExpandTargets_ResolveAllKeepsNames(t *testing.T) {
	cfg := config{resolveAll: true}
	specs := []targetSpec{{Host: "127.0.0.1", Name: "loopback", DSCP: "EF"}}

	got, _, err := expandTargets(specs, nil, cfg)

	if err != nil {
		t.Fatalf("expandTargets: %v", err)
	}
	if !reflect.DeepEqual(got, specs) {
		t.Fatalf("got %#v, want %#v", got, specs)
	}
}
