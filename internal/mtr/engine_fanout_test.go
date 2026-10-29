package mtr

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// trackingProber wraps a fakeProber, counting ProbeHop calls and recording
// the high-water mark of concurrent in-flight ones. delay, if set, is slept
// inside ProbeHop to widen the window in which concurrent calls overlap —
// without it, calls can complete faster than goroutines are scheduled,
// masking real concurrency even without a semaphore.
type trackingProber struct {
	*fakeProber
	delay time.Duration

	calls   atomic.Int32 // total ProbeHop calls
	current atomic.Int32 // calls in flight now
	peak    atomic.Int32 // high-water mark of current
}

func (c *trackingProber) ProbeHop(ctx context.Context, sock HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	c.calls.Add(1)
	n := c.current.Add(1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	defer c.current.Add(-1)
	return c.fakeProber.ProbeHop(ctx, sock, dest, ttl, traceID, timeout)
}

// TestDiscover_BoundsConcurrentProbeHopCalls verifies the F4 fix: discover()
// fans out up to MaxHops goroutines, but the Engine-wide semaphore must cap
// how many ProbeHop calls are actually in flight at once, regardless of how
// many TTLs are being probed.
func TestDiscover_BoundsConcurrentProbeHopCalls(t *testing.T) {
	target := newTestTarget("8.8.8.8")

	tracker := &trackingProber{
		fakeProber: newFakeProber(map[int]pinger.HopReply{
			30: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
		}),
		delay: 20 * time.Millisecond,
	}

	cfg := Config{
		MaxHops:             30,
		ProbeInterval:       time.Hour, // keep a second (steady-state probe) round from firing mid-test
		HopTimeout:          500 * time.Millisecond,
		RediscoverEvery:     time.Hour,
		MaxConcurrentProbes: 4,
	}
	eng := NewEngine(tracker, []*stats.TargetStats{target}, cfg)
	eng.Start()
	defer eng.Stop()

	waitForHops(t, target, 30, 3*time.Second)

	peak := tracker.peak.Load()
	if peak > 4 {
		t.Errorf("peak concurrent ProbeHop calls = %d, want <= 4 (MaxConcurrentProbes)", peak)
	}
	if peak == 0 {
		t.Error("expected at least one ProbeHop call to have been observed")
	}
}

// TestEngine_StopDuringDiscover_NoGoroutineLeak verifies that Stop() during
// an in-flight discover() round — including goroutines still blocked
// waiting to acquire the semaphore — returns promptly without leaking
// goroutines, mirroring cmd/main's TestRunStop_NoGoroutineLeak pattern.
func TestEngine_StopDuringDiscover_NoGoroutineLeak(t *testing.T) {
	target := newTestTarget("8.8.8.8")

	tracker := &trackingProber{
		fakeProber: newFakeProber(nil), // nothing ever responds
		delay:      500 * time.Millisecond,
	}

	cfg := Config{
		MaxHops:             30,
		ProbeInterval:       time.Hour,
		HopTimeout:          2 * time.Second,
		RediscoverEvery:     time.Hour,
		MaxConcurrentProbes: 4,
	}

	before := runtime.NumGoroutine()
	eng := NewEngine(tracker, []*stats.TargetStats{target}, cfg)
	eng.Start()
	// Every semaphore slot is in use, so the remaining TTL goroutines are
	// queued in acquire().
	if !waitUntil(3*time.Second, func() bool { return tracker.current.Load() == 4 }) {
		t.Fatalf("only %d ProbeHop calls in flight, want 4 (MaxConcurrentProbes)", tracker.current.Load())
	}
	eng.Stop() // must return without leaking goroutines stuck in acquire()

	// small slack for unrelated background goroutines
	if !waitUntil(3*time.Second, func() bool { return runtime.NumGoroutine() <= before+2 }) {
		t.Errorf("goroutine leak suspected: before=%d after=%d", before, runtime.NumGoroutine())
	}
}

// TestDiscover_CancelledRoundStillDiscardsResults is a regression guard:
// the semaphore must not interfere with discover()'s existing "discard the
// whole round on cancellation" logic (a round cancelled mid-flight must
// still return hopCount 0 rather than a torn result set).
func TestDiscover_CancelledRoundStillDiscardsResults(t *testing.T) {
	sem := make(chan struct{}, 2)
	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: true},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before discover even starts

	mtrStats := stats.NewMTRStats()
	cfg := Config{MaxHops: 5, HopTimeout: 200 * time.Millisecond}
	cfg = cfg.withDefaults()

	hopCount := discover(ctx, prober, &fakeHopSocket{}, mtrStats, "1.1.1.1", cfg, sem)
	if hopCount != 0 {
		t.Errorf("expected hopCount 0 for a pre-cancelled round, got %d", hopCount)
	}
}
