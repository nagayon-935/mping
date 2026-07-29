package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// lifecycleFakePinger is a pingerController double that records its own lifecycle,
// so a test can assert that every pinger startPinger creates is eventually
// stopped and closed.
type lifecycleFakePinger struct {
	mu      sync.Mutex
	started bool
	stopped bool
	closed  bool
}

func (f *lifecycleFakePinger) Start(interval, timeout time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return nil
}

func (f *lifecycleFakePinger) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}

func (f *lifecycleFakePinger) Wait() {}

func (f *lifecycleFakePinger) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *lifecycleFakePinger) isReleased() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped && f.closed
}

func (f *lifecycleFakePinger) DiscoverMaxPayload(ctx context.Context, dest string, start int, min int, logf func(string)) (int, string, error) {
	return 0, "", nil
}

func (f *lifecycleFakePinger) TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error) {
	return nil, nil
}

func (f *lifecycleFakePinger) SetSource(ip string)                       {}
func (f *lifecycleFakePinger) SetSize(size int)                          {}
func (f *lifecycleFakePinger) SetCount(count int)                        {}
func (f *lifecycleFakePinger) SetResolveInterval(interval time.Duration) {}
func (f *lifecycleFakePinger) SetLogWriter(w io.Writer)                  {}

// MTRProber returns a stub prober rather than nil: cmdResetMTR (like the old
// resetMTR it replaces) starts an mtr.Engine unconditionally whenever it's
// invoked, without checking cfg.mtrEnabled itself — that gating lives at the
// mping.go call site instead (see fakePinger's MTRProber in mping_test.go).
// supervisor_state_test.go's command-table test exercises cmdResetMTR
// directly, bypassing that call-site gate, so the engine's per-target
// goroutine really does call prober.OpenHopSocket. A nil mtr.HopProber is a
// nil interface value, and calling a method on it panics; noopHopProber
// gives it a safe, real implementation that just declines to open a socket.
func (f *lifecycleFakePinger) MTRProber() mtr.HopProber { return noopHopProber{} }

// noopHopProber is a minimal mtr.HopProber that always fails to open a hop
// socket, so an mtr.Engine started against it exits its per-target goroutine
// immediately instead of doing any real probing.
type noopHopProber struct{}

func (noopHopProber) OpenHopSocket(dest string) (mtr.HopSocket, error) {
	return nil, errors.New("noopHopProber: no socket available")
}

func (noopHopProber) ProbeHop(ctx context.Context, sock mtr.HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	return pinger.HopReply{}, errors.New("noopHopProber: no probe available")
}

func (noopHopProber) NextTraceID() int { return 0 }

func (noopHopProber) ASNInfoFor(ip string) pinger.ASNInfo { return pinger.ASNInfo{} }

// TestStartPingerReleasesSupersededPinger used to verify that two concurrent
// startPinger() calls left exactly one live pinger and released the other.
// That scenario required next.Start() to run outside any lock so both
// goroutines could race to create their own pinger before either published
// s.p (see the superseded-pinger fix this test was written for). startPinger
// now delegates to handle() on the single command-loop goroutine
// (supervisor_loop.go), so two concurrent calls are fully serialized by the
// command queue rather than by a lock: whichever do(cmdStart) is dequeued
// second observes state == stateRunning and returns without creating a
// second pinger at all. The race this test exercised can no longer happen.
// TestHandle_RestartReleasesPreviousPinger (supervisor_state_test.go) now
// covers the release-the-old-pinger behavior via the only path that can
// still produce a superseded pinger: a sequential cmdRestart.

// TestSupervisorSerializesConcurrentRestarts replaces
// TestStartPingerReleasesSupersededPinger. Concurrent starts used to race and
// leak the loser's raw socket; now they queue, so the assertion changes from
// "the loser is cleaned up" to "exactly one pinger is live and every earlier
// one was released".
func TestSupervisorSerializesConcurrentRestarts(t *testing.T) {
	var mu sync.Mutex
	var created []*lifecycleFakePinger

	sup := newSupervisor(supervisorConfig{
		makePinger: func(size int) pingerController {
			fp := &lifecycleFakePinger{}
			mu.Lock()
			created = append(created, fp)
			mu.Unlock()
			return fp
		},
		targets:  []*stats.TargetStats{stats.NewTargetStats("example.com")},
		interval: time.Second,
		timeout:  time.Second,
		logCh:    make(chan string, 8),
	})
	sup.Start()
	defer sup.Shutdown()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sup.do(cmdRestart); err != nil {
				t.Errorf("cmdRestart: %v", err)
			}
		}()
	}
	wg.Wait()

	live := sup.pingerSnap.Load()
	if live == nil {
		t.Fatal("no live pinger after restarts")
	}

	mu.Lock()
	all := append([]*lifecycleFakePinger(nil), created...)
	mu.Unlock()

	liveCount := 0
	for _, fp := range all {
		if pingerController(fp) == *live {
			liveCount++
			continue
		}
		if !fp.isReleased() {
			t.Errorf("superseded pinger leaked (stopped=%v closed=%v)", fp.stopped, fp.closed)
		}
	}
	if liveCount != 1 {
		t.Errorf("expected exactly 1 live pinger, got %d of %d", liveCount, len(all))
	}
}
