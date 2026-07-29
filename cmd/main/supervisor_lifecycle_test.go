package main

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
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
func (f *lifecycleFakePinger) MTRProber() mtr.HopProber                  { return nil }

// TestStartPingerReleasesSupersededPinger verifies that concurrent
// startPinger calls leave exactly one live pinger and release every other
// one. startPinger runs next.Start() — which opens the raw ICMP socket and
// spawns the worker goroutines — outside s.mu and then assigns s.p
// last-writer-wins, so without an explicit handoff the loser's socket and
// goroutines leaked and it kept sending ICMP. Reachable by pressing 'S'
// twice while a restart is still in flight (internal/ui/input_handler.go).
func TestStartPingerReleasesSupersededPinger(t *testing.T) {
	const iterations = 50

	for i := 0; i < iterations; i++ {
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
			targets: []*stats.TargetStats{stats.NewTargetStats("example.com")},
			logCh:   make(chan string, 1),
		})

		var wg sync.WaitGroup
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := sup.startPinger(); err != nil {
					t.Errorf("startPinger: %v", err)
				}
			}()
		}
		wg.Wait()

		sup.mu.Lock()
		live := sup.p
		sup.mu.Unlock()

		mu.Lock()
		all := append([]*lifecycleFakePinger(nil), created...)
		mu.Unlock()

		if len(all) != 2 {
			t.Fatalf("iteration %d: expected 2 pingers created, got %d", i, len(all))
		}
		for _, fp := range all {
			if pingerController(fp) == live {
				continue // the survivor stays running by design
			}
			if !fp.isReleased() {
				t.Fatalf("iteration %d: superseded pinger leaked (stopped=%v closed=%v)",
					i, fp.stopped, fp.closed)
			}
		}
	}
}
