package mtr

import (
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// slowLookupProber is a fakeProber whose ASN and PTR lookups block until
// release is closed, modelling slow or unresponsive DNS (Team Cymru TXT and
// reverse lookups).
type slowLookupProber struct {
	*fakeProber
	release chan struct{}
	mu      sync.Mutex
	lookups int
}

func (p *slowLookupProber) wait() {
	p.mu.Lock()
	p.lookups++
	p.mu.Unlock()
	<-p.release
}

func (p *slowLookupProber) ASNInfoFor(ip string) pinger.ASNInfo {
	p.wait()
	return pinger.ASNInfo{Number: "AS" + ip, Country: "JP", Org: "org-" + ip}
}

func (p *slowLookupProber) PTRFor(ip string) string {
	p.wait()
	return "ptr-" + ip
}

func newSlowLookupProber() *slowLookupProber {
	return &slowLookupProber{
		fakeProber: newFakeProber(map[int]pinger.HopReply{
			1: {SrcIP: "10.0.0.1", Responded: true},
			2: {SrcIP: "10.0.0.2", Responded: true},
			3: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
		}),
		release: make(chan struct{}),
	}
}

func annotateTestConfig(pathOnly bool) Config {
	return Config{PathOnly: pathOnly, ProbeInterval: 5 * time.Millisecond, HopTimeout: 10 * time.Millisecond, RediscoverEvery: time.Hour, MaxHops: 4}
}

// TestEngine_HopsAppearBeforeSlowASNAndPTRLookups is the -M -a -r regression:
// discovery used to wait for every hop's ASN and PTR lookup in turn before
// registering the next hop, so hops appeared one by one, seconds apart.
func TestEngine_HopsAppearBeforeSlowASNAndPTRLookups(t *testing.T) {
	for _, pathOnly := range []bool{false, true} {
		name := map[bool]string{false: "mtr", true: "traceroute"}[pathOnly]
		t.Run(name, func(t *testing.T) {
			// Arrange
			target := newTestTarget("8.8.8.8")
			prober := newSlowLookupProber()
			eng := NewEngine(prober, []*stats.TargetStats{target}, annotateTestConfig(pathOnly))

			// Act
			eng.Start()
			defer eng.Stop()
			defer close(prober.release)

			// Assert: every hop IP is visible while all lookups are blocked.
			ok := waitUntil(500*time.Millisecond, func() bool {
				hops := target.MTR().View()
				return len(hops) == 3 && hops[0].IP == "10.0.0.1" && hops[1].IP == "10.0.0.2" && hops[2].IP == "8.8.8.8"
			})
			if !ok {
				t.Fatalf("hops = %+v, want all three IPs before any lookup finished", target.MTR().View())
			}
		})
	}
}

// TestEngine_AnnotatesHopsOnceLookupsFinish verifies the deferred lookups
// still land on the hop they were made for.
func TestEngine_AnnotatesHopsOnceLookupsFinish(t *testing.T) {
	// Arrange
	target := newTestTarget("8.8.8.8")
	prober := newSlowLookupProber()
	eng := NewEngine(prober, []*stats.TargetStats{target}, annotateTestConfig(false))
	eng.Start()
	defer eng.Stop()
	waitForHops(t, target, 3, 500*time.Millisecond)

	// Act
	close(prober.release)

	// Assert
	ok := waitUntil(500*time.Millisecond, func() bool {
		hops := target.MTR().View()
		if len(hops) != 3 {
			return false
		}
		for _, h := range hops {
			if h.ASN != "AS"+h.IP || h.Org != "org-"+h.IP || h.PTR != "ptr-"+h.IP {
				return false
			}
		}
		return true
	})
	if !ok {
		t.Fatalf("hops = %+v, want each hop annotated with its own ASN/Org/PTR", target.MTR().View())
	}
}

// TestEngine_StopDoesNotHangOnBlockedLookups verifies Stop returns promptly
// while lookups are still blocked, and that no annotation is written after it.
func TestEngine_StopDoesNotHangOnBlockedLookups(t *testing.T) {
	// Arrange
	target := newTestTarget("8.8.8.8")
	prober := newSlowLookupProber()
	eng := NewEngine(prober, []*stats.TargetStats{target}, annotateTestConfig(false))
	eng.Start()
	waitForHops(t, target, 3, 500*time.Millisecond)

	// Act
	stopped := make(chan struct{})
	go func() { eng.Stop(); close(stopped) }()

	// Assert
	select {
	case <-stopped:
	case <-time.After(time.Second):
		close(prober.release)
		t.Fatal("Stop blocked on in-flight ASN/PTR lookups")
	}
	close(prober.release)
	time.Sleep(20 * time.Millisecond)
	for _, h := range target.MTR().View() {
		if h.ASN != "" || h.PTR != "" {
			t.Fatalf("hop %d annotated after Stop: %+v", h.TTL, h)
		}
	}
}

// TestEngine_LooksUpEachHopIPOnce verifies repeated probe rounds do not
// re-issue lookups for an IP already resolved or in flight.
func TestEngine_LooksUpEachHopIPOnce(t *testing.T) {
	// Arrange
	target := newTestTarget("8.8.8.8")
	prober := newSlowLookupProber()
	close(prober.release)
	eng := NewEngine(prober, []*stats.TargetStats{target}, annotateTestConfig(false))

	// Act
	eng.Start()
	waitUntil(500*time.Millisecond, func() bool { return firstHopSent(target) >= 10 })
	eng.Stop()

	// Assert: one ASN + one PTR lookup per distinct hop IP.
	prober.mu.Lock()
	defer prober.mu.Unlock()
	if prober.lookups != 6 {
		t.Fatalf("lookups = %d, want 6 (ASN+PTR for 3 hop IPs)", prober.lookups)
	}
}
