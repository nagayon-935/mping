package main

import (
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

const (
	linkLocalDst = "fe80::be24:11ff:febe:6d92%en0"
	linkLocalSrc = "fe80::1c3f:1632:ffc3:5a7f"
	globalDst    = "2606:4700:4700::1111"
	globalSrc    = "2001:db8:2:3::a9"
)

// kernelRoutes answers a route probe the way a dual-stack host with a
// link-local and a global IPv6 address does: each destination gets the
// source of its own scope.
func kernelRoutes(remote, network string) string {
	switch {
	case network == "udp6" && remote == linkLocalDst:
		return linkLocalSrc
	case network == "udp6" && remote == globalDst:
		return globalSrc
	case network == "udp4" && remote == "192.0.2.1":
		return "10.0.0.2"
	}
	return ""
}

func TestSourceHintsGiveEachDestinationItsRouteSelectedSource(t *testing.T) {
	stubOutboundIP(t, kernelRoutes)
	// The session-wide hint came from the first target, a link-local one.
	h := newSourceHints(config{}, runEnv{dispV6: linkLocalSrc})

	tests := []struct{ dst, want string }{
		{linkLocalDst, linkLocalSrc},
		{globalDst, globalSrc},
		{"192.0.2.1", "10.0.0.2"},
	}
	for _, tt := range tests {
		if got := h.forDst(tt.dst); got != tt.want {
			t.Errorf("forDst(%q) = %q, want %q", tt.dst, got, tt.want)
		}
	}
}

func TestSourceHintsProbeEachDestinationOnceWithinTTL(t *testing.T) {
	probes := 0
	stubOutboundIP(t, func(remote, network string) string {
		probes++
		return kernelRoutes(remote, network)
	})
	now := time.Unix(1000, 0)
	h := newSourceHints(config{}, runEnv{})
	h.now = func() time.Time { return now }

	h.forDst(globalDst)
	h.forDst(globalDst)
	if probes != 1 {
		t.Fatalf("probes within TTL = %d, want 1", probes)
	}

	now = now.Add(sourceHintTTL + time.Second)
	h.forDst(globalDst)
	if probes != 2 {
		t.Fatalf("probes after TTL = %d, want 2 (address may have rotated)", probes)
	}
}

func TestSourceHintsLeaveFallbackToCaller(t *testing.T) {
	probed := false
	stubOutboundIP(t, func(string, string) string { probed = true; return "" })

	tests := []struct {
		name      string
		cfg       config
		env       runEnv
		dst       string
		wantProbe bool
	}{
		{"unresolved hostname has no address to probe", config{}, runEnv{}, "", false},
		{"hostname text is not probed", config{}, runEnv{}, "example.com", false},
		{"no route answers empty", config{}, runEnv{}, globalDst, true},
		{"explicit -S keeps its fixed source", config{sourceAddr: "2001:db8::5"}, runEnv{bindIP: "2001:db8::5", dispV6: "2001:db8::5"}, globalDst, false},
		{"single-family -I keeps its bound source", config{ifaceName: "en0", ipv6Only: true}, runEnv{bindIP: "2001:db8::6", dispV6: "2001:db8::6"}, globalDst, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probed = false
			h := newSourceHints(tt.cfg, tt.env)

			if got := h.forDst(tt.dst); got != "" {
				t.Errorf("forDst(%q) = %q, want empty so the family hint is shown", tt.dst, got)
			}
			if probed != tt.wantProbe {
				t.Errorf("probe consulted = %v, want %v", probed, tt.wantProbe)
			}
		})
	}
}

func TestSourceHintsDualStackInterfaceProbesThroughTheInterface(t *testing.T) {
	origIfaceIP := getInterfaceIPFn
	getInterfaceIPFn = func(string, bool) (string, error) { return "", nil }
	t.Cleanup(func() { getInterfaceIPFn = origIfaceIP })
	var gotBind pinger.BindConfig
	orig := getPreferredOutboundIPBoundFn
	getPreferredOutboundIPBoundFn = func(remote, network string, bind pinger.BindConfig) string {
		gotBind = bind
		return kernelRoutes(remote, network)
	}
	t.Cleanup(func() { getPreferredOutboundIPBoundFn = orig })

	h := newSourceHints(config{ifaceName: "en0"}, runEnv{dispV6: linkLocalSrc})

	if got := h.forDst(globalDst); got != globalSrc {
		t.Errorf("forDst(%q) = %q, want %q", globalDst, got, globalSrc)
	}
	if gotBind.Interface != "en0" {
		t.Errorf("probe bind interface = %q, want en0", gotBind.Interface)
	}
}

func TestNilSourceHintsAnswerEmpty(t *testing.T) {
	var h *sourceHints
	if got := h.forDst(globalDst); got != "" {
		t.Errorf("nil forDst = %q, want empty", got)
	}
}

func TestCaptureReportRecordsEachTargetsSource(t *testing.T) {
	stubOutboundIP(t, kernelRoutes)
	a, b := stats.NewTargetStats(linkLocalDst), stats.NewTargetStats(globalDst)
	a.SetIP(linkLocalDst)
	b.SetIP(globalDst)
	s := newSupervisor(supervisorConfig{
		targets: []*stats.TargetStats{a, b}, specs: []targetSpec{{Host: linkLocalDst}, {Host: globalDst}},
		sourceIPv6: linkLocalSrc, sources: newSourceHints(config{}, runEnv{dispV6: linkLocalSrc}),
	})

	r, err := s.captureReport(0)
	if err != nil {
		t.Fatal(err)
	}

	if got := r.Settings.SourceIPv6Hint; got != linkLocalSrc {
		t.Errorf("settings source_ipv6_hint = %q, want the session hint %q", got, linkLocalSrc)
	}
	for i, want := range []string{linkLocalSrc, globalSrc} {
		if got := r.Targets[i].SourceIP; got != want {
			t.Errorf("target %d source_ip = %q, want %q", i, got, want)
		}
	}
}
