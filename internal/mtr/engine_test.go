package mtr

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// fakeHopSocket is a no-op HopSocket stand-in for tests.
type fakeHopSocket struct{}

func (f *fakeHopSocket) Close() {}

// fakeProber controls which hops respond and with what RTT.
type fakeProber struct {
	mu      sync.Mutex
	replies map[int]pinger.HopReply // keyed by ttl
	idSeq   int
}

func newFakeProber(replies map[int]pinger.HopReply) *fakeProber {
	return &fakeProber{replies: replies}
}

// setReplies swaps the reply table, simulating a route change.
func (f *fakeProber) setReplies(replies map[int]pinger.HopReply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = replies
}

func (f *fakeProber) OpenHopSocket(ctx context.Context, dest string) (HopSocket, error) {
	return &fakeHopSocket{}, nil
}

func (f *fakeProber) ProbeHop(_ context.Context, _ HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.replies[ttl]; ok {
		return r, nil
	}
	return pinger.HopReply{Responded: false}, nil
}

func (f *fakeProber) NextTraceID() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idSeq++
	return f.idSeq
}

func (f *fakeProber) ASNInfoFor(ip string) pinger.ASNInfo { return pinger.ASNInfo{} }

// newTestTarget returns TargetStats for host with its IP already resolved.
func newTestTarget(host string) *stats.TargetStats {
	ts := stats.NewTargetStats(host)
	ts.SetIP(host)
	return ts
}

// waitUntil polls cond every 5ms until it holds or timeout elapses, and
// reports whether it held.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(timeout); ; time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// waitForHops blocks until MTRStats has at least n hops or timeout.
func waitForHops(t *testing.T, ts *stats.TargetStats, n int, timeout time.Duration) {
	t.Helper()
	if !waitUntil(timeout, func() bool { return len(ts.MTR().View()) >= n }) {
		t.Errorf("timed out waiting for %d hops; got %d", n, len(ts.MTR().View()))
	}
}

// firstHopSent returns how many probes the first hop has sent so far.
func firstHopSent(ts *stats.TargetStats) int {
	if hops := ts.MTR().View(); len(hops) > 0 {
		return hops[0].Sent
	}
	return 0
}

// waitForSent blocks until the first hop has sent at least minSent probes or timeout.
func waitForSent(t *testing.T, ts *stats.TargetStats, minSent int, timeout time.Duration) {
	t.Helper()
	if !waitUntil(timeout, func() bool { return firstHopSent(ts) >= minSent }) {
		t.Errorf("timed out waiting for sent>=%d; got %d", minSent, firstHopSent(ts))
	}
}

func TestEngine_CleanPath(t *testing.T) {
	target := newTestTarget("8.8.8.8")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: false},
		2: {SrcIP: "10.0.0.2", Responded: true, ReachedDest: false},
		3: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})

	cfg := Config{
		ProbeInterval:   20 * time.Millisecond,
		HopTimeout:      50 * time.Millisecond,
		RediscoverEvery: 10 * time.Minute,
		MaxHops:         5,
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()

	waitForHops(t, target, 3, 500*time.Millisecond)
	waitForSent(t, target, 3, 500*time.Millisecond)

	eng.Stop()

	hops := target.MTR().View()
	if len(hops) != 3 {
		t.Fatalf("want 3 hops, got %d", len(hops))
	}
	if hops[0].IP != "10.0.0.1" {
		t.Errorf("hop1 IP: want 10.0.0.1, got %q", hops[0].IP)
	}
	if hops[2].IP != "8.8.8.8" {
		t.Errorf("hop3 IP: want 8.8.8.8, got %q", hops[2].IP)
	}
}

// TestEngine_ProbesUnknownHops verifies that a hop that never responds
// (a "*" in the middle of the path) stays in the table with no IP, keeps
// being probed, and accumulates full loss while its neighbors respond.
func TestEngine_ProbesUnknownHops(t *testing.T) {
	target := newTestTarget("8.8.8.8")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true},
		// hop 2 never responds during discovery or after (stays as *)
		3: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})

	cfg := Config{
		ProbeInterval:   15 * time.Millisecond,
		HopTimeout:      30 * time.Millisecond,
		RediscoverEvery: 10 * time.Minute,
		MaxHops:         5,
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()

	waitForHops(t, target, 3, 500*time.Millisecond)
	// Let several continuous probe ticks elapse (hop1/hop3 keep responding).
	waitForSent(t, target, 5, 500*time.Millisecond)
	eng.Stop()

	hops := target.MTR().View()
	if len(hops) < 3 {
		t.Fatalf("want at least 3 hops, got %d", len(hops))
	}
	if hops[1].IP != "" {
		t.Fatalf("hop2 IP: want empty (* hop), got %q", hops[1].IP)
	}
	if hops[1].Recv != 0 {
		t.Errorf("hop2 Recv: want 0, got %d", hops[1].Recv)
	}
	if hops[1].Sent == 0 || hops[1].LossPct != 100 {
		t.Errorf("unknown hop must accumulate attempts and loss: %+v", hops[1])
	}
}

func TestProbe_UnknownHopRecoversWithoutRediscovery(t *testing.T) {
	prober := newFakeProber(map[int]pinger.HopReply{
		3: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})
	m := stats.NewTargetStats("8.8.8.8").MTR()
	cfg := (&Config{MaxHops: 5}).withDefaults()
	ctx := context.Background()
	sock := &fakeHopSocket{}
	n := discover(ctx, prober, sock, m, "8.8.8.8", cfg, nil)
	probe(ctx, prober, sock, m, "8.8.8.8", n, cfg, nil)
	prober.replies[2] = pinger.HopReply{SrcIP: "10.0.0.2", Responded: true, RTT: 5 * time.Millisecond}
	probe(ctx, prober, sock, m, "8.8.8.8", n, cfg, nil)
	h := m.View()[1]
	if h.IP != "10.0.0.2" || h.Sent != 2 || h.Recv != 1 || h.LossPct != 50 {
		t.Fatalf("hop should recover on the next probe and retain earlier loss: %+v", h)
	}
}

type failedDiscoveryProber struct{ *fakeProber }

func (p failedDiscoveryProber) ProbeHop(context.Context, HopSocket, string, int, int, time.Duration) (pinger.HopReply, error) {
	return pinger.HopReply{}, errors.New("temporary send error")
}

func TestDiscover_SendErrorsLeaveProbeableSlots(t *testing.T) {
	prober := newFakeProber(map[int]pinger.HopReply{
		3: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})
	m := stats.NewTargetStats("8.8.8.8").MTR()
	cfg := (&Config{MaxHops: 3}).withDefaults()
	ctx := context.Background()
	sock := &fakeHopSocket{}
	n := discover(ctx, failedDiscoveryProber{prober}, sock, m, "8.8.8.8", cfg, nil)
	if n != 3 || len(m.View()) != 3 {
		t.Fatalf("failed discovery must retain all TTL slots: count=%d, hops=%+v", n, m.View())
	}
	probe(ctx, prober, sock, m, "8.8.8.8", n, cfg, nil)
	if h := m.View()[2]; h.IP != "8.8.8.8" || h.Recv != 1 {
		t.Fatalf("slot must accept a later response: %+v", h)
	}
}

// TestEngine_StopFreezesStats verifies that no further probes are recorded
// once Stop has returned.
func TestEngine_StopFreezesStats(t *testing.T) {
	target := newTestTarget("1.1.1.1")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: true},
	})

	cfg := Config{
		ProbeInterval:   10 * time.Millisecond,
		HopTimeout:      20 * time.Millisecond,
		RediscoverEvery: 10 * time.Minute,
		MaxHops:         3,
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()
	waitForSent(t, target, 1, 2*time.Second)
	eng.Stop()

	sentAtStop := firstHopSent(target)
	if sentAtStop == 0 {
		t.Fatal("no probe was recorded before Stop")
	}
	// Several probe intervals: a still-running engine would send more.
	time.Sleep(30 * time.Millisecond)
	if sentNow := firstHopSent(target); sentNow != sentAtStop {
		t.Errorf("engine still running after Stop: sent changed from %d to %d", sentAtStop, sentNow)
	}
}

func TestEngine_MultipleTargets(t *testing.T) {
	t1 := newTestTarget("8.8.8.8")
	t2 := newTestTarget("1.1.1.1")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: true},
	})

	cfg := Config{
		ProbeInterval:   20 * time.Millisecond,
		HopTimeout:      30 * time.Millisecond,
		RediscoverEvery: 10 * time.Minute,
		MaxHops:         3,
	}
	eng := NewEngine(prober, []*stats.TargetStats{t1, t2}, cfg)
	eng.Start()

	waitForSent(t, t1, 2, 500*time.Millisecond)
	waitForSent(t, t2, 2, 500*time.Millisecond)
	eng.Stop()

	for _, ts := range []*stats.TargetStats{t1, t2} {
		hops := ts.MTR().View()
		if len(hops) == 0 {
			t.Errorf("%s: no hops", ts.Host)
		}
	}
}

func TestEngine_OnFlap_CalledOnRouteChange(t *testing.T) {
	target := newTestTarget("8.8.8.8")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: true},
	})

	var flapMu sync.Mutex
	var flapHost, flapDesc string
	flapCalled := make(chan struct{}, 1)

	cfg := Config{
		ProbeInterval:   10 * time.Millisecond,
		HopTimeout:      20 * time.Millisecond,
		RediscoverEvery: 30 * time.Millisecond, // very short for test
		MaxHops:         3,
		OnFlap: func(host, desc string) {
			flapMu.Lock()
			flapHost = host
			flapDesc = desc
			flapMu.Unlock()
			select {
			case flapCalled <- struct{}{}:
			default:
			}
		},
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()

	// Wait for initial discovery
	waitForHops(t, target, 1, 500*time.Millisecond)

	// Change the route
	prober.setReplies(map[int]pinger.HopReply{
		1: {SrcIP: "192.168.99.1", Responded: true, ReachedDest: true},
	})

	// Wait for OnFlap to fire (rediscover ticker fires every 30ms)
	select {
	case <-flapCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("OnFlap was not called within timeout")
	}

	eng.Stop()

	flapMu.Lock()
	defer flapMu.Unlock()
	if flapHost != "8.8.8.8" {
		t.Errorf("OnFlap host: want 8.8.8.8, got %q", flapHost)
	}
	if flapDesc == "" {
		t.Error("OnFlap desc should not be empty")
	}
}

// TestEngine_RediscoverShortensPath_DropsStaleHops verifies that when a
// rediscovery finds a shorter path than before, the trailing hops from the
// old (longer) path are dropped rather than left frozen in MTRStats forever.
func TestEngine_RediscoverShortensPath_DropsStaleHops(t *testing.T) {
	target := newTestTarget("8.8.8.8")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: false},
		2: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})

	cfg := Config{
		ProbeInterval:   10 * time.Millisecond,
		HopTimeout:      20 * time.Millisecond,
		RediscoverEvery: 30 * time.Millisecond, // very short for test
		MaxHops:         5,
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()

	// Wait for the initial 2-hop discovery.
	waitForHops(t, target, 2, 500*time.Millisecond)

	// Shorten the path: destination now reachable in a single hop.
	prober.setReplies(map[int]pinger.HopReply{
		1: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})

	// Wait for a rediscover cycle to pick up the shorter path.
	waitUntil(500*time.Millisecond, func() bool { return len(target.MTR().View()) == 1 })

	eng.Stop()

	hops := target.MTR().View()
	if len(hops) != 1 {
		t.Fatalf("want 1 hop after path shortened, got %d (stale trailing hops not dropped)", len(hops))
	}
	if hops[0].IP != "8.8.8.8" {
		t.Errorf("hop1 IP: want 8.8.8.8, got %q", hops[0].IP)
	}
}

func TestEngine_OnFlap_NotCalledOnSameRoute(t *testing.T) {
	target := newTestTarget("1.1.1.1")

	// Steady probe ticks are off, so every ProbeHop call comes from a
	// discovery round (MaxHops calls each) and the call count says how many
	// rediscoveries have run.
	const maxHops = 3
	prober := &trackingProber{fakeProber: newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: true},
	})}

	flapCalled := false
	cfg := Config{
		ProbeInterval:   time.Hour,
		HopTimeout:      20 * time.Millisecond,
		RediscoverEvery: 30 * time.Millisecond,
		MaxHops:         maxHops,
		OnFlap: func(host, desc string) {
			flapCalled = true
		},
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()

	// Initial discovery plus three rediscoveries: at least two of them have
	// completed and compared their route with the previous one.
	if !waitUntil(2*time.Second, func() bool { return prober.calls.Load() >= 4*maxHops }) {
		t.Fatalf("only %d ProbeHop calls; rediscovery is not running", prober.calls.Load())
	}
	eng.Stop()

	if flapCalled {
		t.Error("OnFlap should not be called when route is unchanged")
	}
}

func TestEngineTargetEditsKeepSurvivingRunAndSharedLimit(t *testing.T) {
	a, b := stats.NewTargetStats("a"), stats.NewTargetStats("b")
	prober := newFakeProber(map[int]pinger.HopReply{1: {SrcIP: "127.0.0.1", Responded: true, ReachedDest: true}})
	e := NewEngine(prober, []*stats.TargetStats{a}, Config{MaxHops: 1, ProbeInterval: 10 * time.Millisecond, MaxConcurrentProbes: 2})
	e.Start()
	defer e.Stop()
	waitForSent(t, a, 2, time.Second)
	e.mu.Lock()
	original := e.targetRuns[a]
	e.mu.Unlock()
	before := a.MTR().View()[0].Sent
	e.AddTarget(b)
	waitForSent(t, b, 2, time.Second)
	e.RemoveTarget(b)
	e.mu.Lock()
	current := e.targetRuns[a]
	e.mu.Unlock()
	if current != original || cap(e.sem) != 2 || a.MTR().View()[0].Sent < before {
		t.Fatal("surviving run or shared limit changed")
	}
}
