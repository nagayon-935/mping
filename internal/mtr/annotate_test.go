package mtr

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// slowLookupProber is a fakeProber whose ASN and PTR lookups block until
// release is closed or the lookup's context is cancelled, modelling slow or
// unresponsive DNS (Team Cymru TXT and reverse lookups). failFirst makes the
// first lookup of each kind per IP return nothing, like a transient timeout.
type slowLookupProber struct {
	*fakeProber
	release   chan struct{}
	failFirst bool

	mu       sync.Mutex
	lookups  int
	active   int
	afterEnd int // lookups started after ended was set
	ended    bool
	seen     map[string]bool
}

func (p *slowLookupProber) begin(kind, ip string) (fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lookups++
	p.active++
	if p.ended {
		p.afterEnd++
	}
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	key := kind + ip
	fail = p.failFirst && !p.seen[key]
	p.seen[key] = true
	return fail
}

func (p *slowLookupProber) end() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
}

func (p *slowLookupProber) block(ctx context.Context) bool {
	select {
	case <-p.release:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *slowLookupProber) ASNInfoFor(ctx context.Context, ip string) pinger.ASNInfo {
	fail := p.begin("asn", ip)
	defer p.end()
	if !p.block(ctx) || fail {
		return pinger.ASNInfo{}
	}
	return pinger.ASNInfo{Number: "AS" + ip, Country: "JP", Org: "org-" + ip}
}

func (p *slowLookupProber) PTRFor(ctx context.Context, ip string) string {
	fail := p.begin("ptr", ip)
	defer p.end()
	if !p.block(ctx) || fail {
		return ""
	}
	return "ptr-" + ip
}

func (p *slowLookupProber) snapshot() (lookups, active, afterEnd int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lookups, p.active, p.afterEnd
}

func (p *slowLookupProber) markEnded() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = true
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
	return Config{PathOnly: pathOnly, ProbeInterval: 5 * time.Millisecond, HopTimeout: 10 * time.Millisecond,
		RediscoverEvery: time.Hour, MaxHops: 4, AnnotationRetry: 20 * time.Millisecond}
}

func hopsFullyAnnotated(target *stats.TargetStats) bool {
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
	if !waitUntil(500*time.Millisecond, func() bool { return hopsFullyAnnotated(target) }) {
		t.Fatalf("hops = %+v, want each hop annotated with its own ASN/Org/PTR", target.MTR().View())
	}
}

// TestEngine_RetriesFailedLookups verifies a transient lookup failure is not
// remembered for the target's lifetime: pinger deliberately does not cache
// failures, so the annotator must not either.
func TestEngine_RetriesFailedLookups(t *testing.T) {
	for _, pathOnly := range []bool{false, true} {
		name := map[bool]string{false: "mtr", true: "traceroute"}[pathOnly]
		t.Run(name, func(t *testing.T) {
			// Arrange
			target := newTestTarget("8.8.8.8")
			prober := newSlowLookupProber()
			prober.failFirst = true
			close(prober.release)
			cfg := annotateTestConfig(pathOnly)
			cfg.RediscoverEvery = 10 * time.Millisecond // path-only notes IPs only on discovery
			eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)

			// Act
			eng.Start()
			defer eng.Stop()

			// Assert
			if !waitUntil(time.Second, func() bool { return hopsFullyAnnotated(target) }) {
				t.Fatalf("hops = %+v, want annotations after the failed first lookups were retried", target.MTR().View())
			}
		})
	}
}

// TestEngine_StopCancelsAndJoinsLookups verifies Stop cancels blocked lookups
// and waits for them, so no lookup goroutine outlives the engine and the
// prober is never called after Stop returns.
func TestEngine_StopCancelsAndJoinsLookups(t *testing.T) {
	// Arrange
	target := newTestTarget("8.8.8.8")
	prober := newSlowLookupProber()
	eng := NewEngine(prober, []*stats.TargetStats{target}, annotateTestConfig(false))
	eng.Start()
	waitForHops(t, target, 3, 500*time.Millisecond)
	waitUntil(500*time.Millisecond, func() bool { _, active, _ := prober.snapshot(); return active > 0 })

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
	prober.markEnded()
	_, active, _ := prober.snapshot()
	if active != 0 {
		t.Fatalf("%d lookup(s) still running after Stop returned", active)
	}
	close(prober.release)
	time.Sleep(30 * time.Millisecond)
	if _, _, afterEnd := prober.snapshot(); afterEnd != 0 {
		t.Fatalf("%d lookup(s) started after Stop returned", afterEnd)
	}
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
	ok := waitUntil(time.Second, func() bool { return firstHopSent(target) >= 10 })
	eng.Stop()

	// Assert: one ASN + one PTR lookup per distinct hop IP.
	if !ok {
		t.Fatalf("only %d probe rounds ran, want >= 10", firstHopSent(target))
	}
	if lookups, _, _ := prober.snapshot(); lookups != 6 {
		t.Fatalf("lookups = %d, want 6 (ASN+PTR for 3 hop IPs)", lookups)
	}
}

// TestProbe_ECMPHopKeepsAnnotationsWhenResponderAlternates verifies a hop
// whose responder alternates between two routers (ECMP) is recorded with the
// new router's cached annotations in the same update, never left blank.
func TestProbe_ECMPHopKeepsAnnotationsWhenResponderAlternates(t *testing.T) {
	// Arrange
	m := stats.NewMTRStats()
	m.EnsureLen(1)
	prober := newFakeProber(map[int]pinger.HopReply{1: {SrcIP: "10.0.0.1", Responded: true}})
	ann := newHopAnnotator(context.Background(), prober, m, time.Hour)
	defer ann.close()
	ann.mu.Lock()
	ann.rememberLocked("10.0.0.1", pinger.ASNInfo{Number: "AS1"}, "a.example")
	ann.rememberLocked("10.0.0.2", pinger.ASNInfo{Number: "AS2"}, "b.example")
	ann.mu.Unlock()
	cfg := annotateTestConfig(false)

	for round, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.1", "10.0.0.2"} {
		prober.setReplies(map[int]pinger.HopReply{1: {SrcIP: ip, Responded: true}})
		gen := stats.Generation()

		// Act
		probe(context.Background(), prober, &fakeHopSocket{}, m, "8.8.8.8", 1, cfg, nil, ann)

		// Assert
		h := m.View()[0]
		want := map[string][2]string{"10.0.0.1": {"AS1", "a.example"}, "10.0.0.2": {"AS2", "b.example"}}[ip]
		wantASN, wantPTR := want[0], want[1]
		if h.IP != ip || h.ASN != wantASN || h.PTR != wantPTR {
			t.Fatalf("round %d: hop = %+v, want %s with %s/%s", round, h, ip, wantASN, wantPTR)
		}
		if bumps := stats.Generation() - gen; bumps != 1 {
			t.Fatalf("round %d: %d generation bumps for one reply, want 1", round, bumps)
		}
	}
}
