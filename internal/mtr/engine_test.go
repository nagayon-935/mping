package mtr

import (
	"context"
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

func (f *fakeProber) OpenHopSocket(dest string) (HopSocket, error) {
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

// waitForHops blocks until MTRStats has at least n hops or timeout.
func waitForHops(t *testing.T, ts *stats.TargetStats, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(ts.MTR().View()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("timed out waiting for %d hops; got %d", n, len(ts.MTR().View()))
}

func waitForSent(t *testing.T, ts *stats.TargetStats, minSent int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		hops := ts.MTR().View()
		if len(hops) > 0 && hops[0].Sent >= minSent {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	hops := ts.MTR().View()
	sent := 0
	if len(hops) > 0 {
		sent = hops[0].Sent
	}
	t.Errorf("timed out waiting for sent>=%d; got %d", minSent, sent)
}

func TestEngine_CleanPath(t *testing.T) {
	target := stats.NewTargetStats("8.8.8.8")
	target.SetIP("8.8.8.8")

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

func TestEngine_StarHopInMiddle(t *testing.T) {
	target := stats.NewTargetStats("8.8.8.8")
	target.SetIP("8.8.8.8")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true},
		// hop 2 never responds (stays as *)
		3: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})

	cfg := Config{
		ProbeInterval:   20 * time.Millisecond,
		HopTimeout:      30 * time.Millisecond,
		RediscoverEvery: 10 * time.Minute,
		MaxHops:         5,
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()

	waitForHops(t, target, 3, 500*time.Millisecond)
	waitForSent(t, target, 3, 500*time.Millisecond)
	eng.Stop()

	hops := target.MTR().View()
	if len(hops) < 3 {
		t.Fatalf("want at least 3 hops, got %d", len(hops))
	}
	if hops[1].IP != "" {
		t.Errorf("hop2 IP: want empty (* hop), got %q", hops[1].IP)
	}
	if hops[1].Recv != 0 {
		t.Errorf("hop2 Recv: want 0, got %d", hops[1].Recv)
	}
}

func TestEngine_SkipsProbingUnknownHops(t *testing.T) {
	target := stats.NewTargetStats("8.8.8.8")
	target.SetIP("8.8.8.8")

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
	if hops[1].Sent != 0 {
		t.Errorf("hop2 Sent: want 0 (continuous probing must skip unknown-IP hops), got %d", hops[1].Sent)
	}
}

func TestEngine_LossAccumulation(t *testing.T) {
	target := stats.NewTargetStats("1.1.1.1")
	target.SetIP("1.1.1.1")

	callCount := 0
	var callMu sync.Mutex

	// hop1 responds alternately
	origProber := &callCountingProber{
		calls: &callCount,
		mu:    &callMu,
		delegate: newFakeProber(map[int]pinger.HopReply{
			1: {SrcIP: "192.168.1.1", Responded: true, ReachedDest: true},
		}),
	}

	cfg := Config{
		ProbeInterval:   15 * time.Millisecond,
		HopTimeout:      30 * time.Millisecond,
		RediscoverEvery: 10 * time.Minute,
		MaxHops:         3,
	}
	eng := NewEngine(origProber, []*stats.TargetStats{target}, cfg)
	eng.Start()

	waitForSent(t, target, 5, 500*time.Millisecond)
	eng.Stop()

	hops := target.MTR().View()
	if len(hops) == 0 {
		t.Fatal("no hops after probing")
	}
	if hops[0].Sent == 0 {
		t.Error("sent should be > 0")
	}
	if hops[0].LossPct < 0 || hops[0].LossPct > 100 {
		t.Errorf("LossPct out of range: %v", hops[0].LossPct)
	}
}

func TestEngine_CtxCancelStops(t *testing.T) {
	target := stats.NewTargetStats("1.1.1.1")
	target.SetIP("1.1.1.1")

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
	time.Sleep(50 * time.Millisecond)
	eng.Stop()

	// After Stop(), stats should be frozen
	hops1 := target.MTR().View()
	time.Sleep(30 * time.Millisecond)
	hops2 := target.MTR().View()

	if len(hops1) == 0 {
		t.Skip("engine stopped before first probe (slow CI)")
	}
	if hops1[0].Sent != hops2[0].Sent {
		t.Errorf("engine still running after Stop: sent changed from %d to %d", hops1[0].Sent, hops2[0].Sent)
	}
}

func TestEngine_MultipleTargets(t *testing.T) {
	t1 := stats.NewTargetStats("8.8.8.8")
	t1.SetIP("8.8.8.8")
	t2 := stats.NewTargetStats("1.1.1.1")
	t2.SetIP("1.1.1.1")

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

// callCountingProber wraps a fakeProber for counting.
type callCountingProber struct {
	calls    *int
	mu       *sync.Mutex
	delegate *fakeProber
}

func (c *callCountingProber) OpenHopSocket(dest string) (HopSocket, error) {
	return c.delegate.OpenHopSocket(dest)
}
func (c *callCountingProber) ProbeHop(ctx context.Context, sock HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	c.mu.Lock()
	*c.calls++
	c.mu.Unlock()
	return c.delegate.ProbeHop(ctx, sock, dest, ttl, traceID, timeout)
}
func (c *callCountingProber) NextTraceID() int                    { return c.delegate.NextTraceID() }
func (c *callCountingProber) ASNInfoFor(ip string) pinger.ASNInfo { return pinger.ASNInfo{} }

// switchingProber returns different hop IPs on the Nth call group to simulate route change.
type switchingProber struct {
	mu         sync.Mutex
	callGroups int // incremented each time setReplies is called
	inner      *fakeProber
}

func newSwitchingProber(initial map[int]pinger.HopReply) *switchingProber {
	return &switchingProber{inner: newFakeProber(initial)}
}

func (s *switchingProber) setReplies(r map[int]pinger.HopReply) {
	s.mu.Lock()
	s.inner.mu.Lock()
	s.inner.replies = r
	s.inner.mu.Unlock()
	s.callGroups++
	s.mu.Unlock()
}

func (s *switchingProber) OpenHopSocket(dest string) (HopSocket, error) {
	return s.inner.OpenHopSocket(dest)
}
func (s *switchingProber) ProbeHop(ctx context.Context, sock HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	return s.inner.ProbeHop(ctx, sock, dest, ttl, traceID, timeout)
}
func (s *switchingProber) NextTraceID() int                    { return s.inner.NextTraceID() }
func (s *switchingProber) ASNInfoFor(ip string) pinger.ASNInfo { return pinger.ASNInfo{} }

func TestEngine_OnFlap_CalledOnRouteChange(t *testing.T) {
	target := stats.NewTargetStats("8.8.8.8")
	target.SetIP("8.8.8.8")

	prober := newSwitchingProber(map[int]pinger.HopReply{
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
	target := stats.NewTargetStats("8.8.8.8")
	target.SetIP("8.8.8.8")

	prober := newSwitchingProber(map[int]pinger.HopReply{
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
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && len(target.MTR().View()) != 1 {
		time.Sleep(5 * time.Millisecond)
	}

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
	target := stats.NewTargetStats("1.1.1.1")
	target.SetIP("1.1.1.1")

	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: true},
	})

	flapCalled := false
	cfg := Config{
		ProbeInterval:   10 * time.Millisecond,
		HopTimeout:      20 * time.Millisecond,
		RediscoverEvery: 30 * time.Millisecond,
		MaxHops:         3,
		OnFlap: func(host, desc string) {
			flapCalled = true
		},
	}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()

	// Wait for at least two re-discovers to happen
	time.Sleep(120 * time.Millisecond)
	eng.Stop()

	if flapCalled {
		t.Error("OnFlap should not be called when route is unchanged")
	}
}
