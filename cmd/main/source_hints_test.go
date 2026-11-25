package main

import (
	"sync"
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
		if got := h.resolve(tt.dst); got != tt.want {
			t.Errorf("resolve(%q) = %q, want %q", tt.dst, got, tt.want)
		}
	}
}

func TestSourceHintsLookupNeverWaitsForTheProbe(t *testing.T) {
	release := make(chan struct{})
	stubOutboundIP(t, func(remote, network string) string {
		<-release
		return kernelRoutes(remote, network)
	})
	h := newSourceHints(config{}, runEnv{})

	// The TUI draw goroutine calls lookup: a probe still running must not
	// stall the frame, so the first answer is empty (family hint shown).
	if got := h.lookup(globalDst); got != "" {
		t.Fatalf("lookup before the probe finished = %q, want empty", got)
	}
	close(release)
	h.wait()

	if got := h.lookup(globalDst); got != globalSrc {
		t.Errorf("lookup after the probe = %q, want %q", got, globalSrc)
	}
}

func TestSourceHintsLookupProbesEachDestinationOnceAtATime(t *testing.T) {
	var mu sync.Mutex
	probes := 0
	release := make(chan struct{})
	stubOutboundIP(t, func(remote, network string) string {
		mu.Lock()
		probes++
		mu.Unlock()
		<-release
		return kernelRoutes(remote, network)
	})
	h := newSourceHints(config{}, runEnv{})

	for range 10 {
		h.lookup(globalDst)
	}
	close(release)
	h.wait()

	if probes != 1 {
		t.Errorf("probes for 10 lookups while one was running = %d, want 1", probes)
	}
}

func TestSourceHintsLookupKeepsTheLastAnswerWhileRefreshing(t *testing.T) {
	answer := globalSrc
	stubOutboundIP(t, func(string, string) string { return answer })
	now := time.Unix(1000, 0)
	h := newSourceHints(config{}, runEnv{})
	h.now = func() time.Time { return now }
	h.resolve(globalDst)

	answer = "2001:db8:2:3::b0" // temporary address rotated
	now = now.Add(sourceHintTTL + time.Second)

	if got := h.lookup(globalDst); got != globalSrc {
		t.Errorf("lookup of an expired entry = %q, want the previous %q until the refresh lands", got, globalSrc)
	}
	h.wait()
	if got := h.lookup(globalDst); got != answer {
		t.Errorf("lookup after the refresh = %q, want %q", got, answer)
	}
}

func TestSourceHintsRetryAFailedProbeSooner(t *testing.T) {
	probes := 0
	stubOutboundIP(t, func(string, string) string { probes++; return "" })
	now := time.Unix(1000, 0)
	h := newSourceHints(config{}, runEnv{})
	h.now = func() time.Time { return now }

	h.resolve(globalDst)
	now = now.Add(sourceMissTTL + time.Second)
	h.resolve(globalDst)

	if sourceMissTTL >= sourceHintTTL {
		t.Fatalf("sourceMissTTL %v should be shorter than sourceHintTTL %v", sourceMissTTL, sourceHintTTL)
	}
	if probes != 2 {
		t.Errorf("probes = %d, want 2: a failed probe (route not up yet) is retried after sourceMissTTL", probes)
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

	h.resolve(globalDst)
	h.resolve(globalDst)
	if probes != 1 {
		t.Fatalf("probes within TTL = %d, want 1", probes)
	}

	now = now.Add(sourceHintTTL + time.Second)
	h.resolve(globalDst)
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

			if got := h.resolve(tt.dst); got != "" {
				t.Errorf("resolve(%q) = %q, want empty so the family hint is shown", tt.dst, got)
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

	if got := h.resolve(globalDst); got != globalSrc {
		t.Errorf("resolve(%q) = %q, want %q", globalDst, got, globalSrc)
	}
	if gotBind.Interface != "en0" {
		t.Errorf("probe bind interface = %q, want en0", gotBind.Interface)
	}
}

func TestNilSourceHintsAnswerEmpty(t *testing.T) {
	var h *sourceHints
	if got := h.resolve(globalDst) + h.lookup(globalDst); got != "" {
		t.Errorf("nil resolve/lookup = %q, want empty", got)
	}
	h.wait()
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
