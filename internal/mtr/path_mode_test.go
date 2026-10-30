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

func TestEngine_PathOnlyDiscoversWithoutProbing(t *testing.T) {
	target := newTestTarget("8.8.8.8")
	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true},
		2: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
	})
	prober.ptrs = map[string]string{"10.0.0.1": "gw.example"}
	cfg := Config{PathOnly: true, ProbeInterval: time.Millisecond, HopTimeout: 10 * time.Millisecond, RediscoverEvery: time.Hour, MaxHops: 4}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()
	waitForHops(t, target, 2, 500*time.Millisecond)
	time.Sleep(50 * time.Millisecond) // many probe intervals
	eng.Stop()

	if got := prober.probeCalls(); got != cfg.MaxHops {
		t.Fatalf("ProbeHop calls = %d, want %d (one discovery round)", got, cfg.MaxHops)
	}
	hops := target.MTR().View()
	if len(hops) != 2 || hops[0].IP != "10.0.0.1" || hops[1].IP != "8.8.8.8" {
		t.Fatalf("hops = %+v, want 10.0.0.1 then 8.8.8.8", hops)
	}
	for _, h := range hops {
		if h.Sent != 0 {
			t.Errorf("hop %d sent %d probes, want none in path-only mode", h.TTL, h.Sent)
		}
	}
	if hops[0].PTR != "gw.example" || hops[1].PTR != "" {
		t.Errorf("PTR = %q, %q, want gw.example and none", hops[0].PTR, hops[1].PTR)
	}
}

func TestEngine_PathOnlyReportsRouteChanges(t *testing.T) {
	target := newTestTarget("8.8.8.8")
	prober := newFakeProber(map[int]pinger.HopReply{
		1: {SrcIP: "10.0.0.1", Responded: true, ReachedDest: true},
	})
	flapped := make(chan string, 1)
	cfg := Config{PathOnly: true, HopTimeout: 10 * time.Millisecond, RediscoverEvery: 20 * time.Millisecond, MaxHops: 2,
		OnFlap: func(_, desc string) {
			select {
			case flapped <- desc:
			default:
			}
		}}
	eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
	eng.Start()
	defer eng.Stop()
	waitForHops(t, target, 1, 500*time.Millisecond)

	prober.setReplies(map[int]pinger.HopReply{1: {SrcIP: "192.0.2.1", Responded: true, ReachedDest: true}})

	select {
	case desc := <-flapped:
		if desc != "hop 1: 10.0.0.1 → 192.0.2.1" {
			t.Errorf("flap description = %q", desc)
		}
	case <-time.After(time.Second):
		t.Fatal("route change not reported in path-only mode")
	}
}

// failingOpenProber fails OpenHopSocket while failing is set.
type failingOpenProber struct {
	*fakeProber
	mu      sync.Mutex
	failing bool
}

func (p *failingOpenProber) setFailing(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failing = on
}

func (p *failingOpenProber) OpenHopSocket(ctx context.Context, dest string) (HopSocket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failing {
		return nil, errors.New("resolve example.invalid: no such host")
	}
	return &fakeHopSocket{}, nil
}

func TestEngine_RecordsSocketFailureAndRetries(t *testing.T) {
	for _, pathOnly := range []bool{true, false} {
		target := newTestTarget("8.8.8.8")
		prober := &failingOpenProber{fakeProber: newFakeProber(map[int]pinger.HopReply{
			1: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
		}), failing: true}
		cfg := Config{PathOnly: pathOnly, ProbeInterval: 5 * time.Millisecond, HopTimeout: 10 * time.Millisecond, RediscoverEvery: 20 * time.Millisecond, MaxHops: 2}
		eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
		eng.Start()

		if !waitUntil(time.Second, func() bool { return target.MTR().Error() != "" }) {
			t.Fatalf("pathOnly=%v: socket failure not recorded", pathOnly)
		}
		if got := target.MTR().Error(); got != "resolve example.invalid: no such host" {
			t.Errorf("pathOnly=%v: error = %q", pathOnly, got)
		}
		prober.setFailing(false)
		if !waitUntil(time.Second, func() bool { return len(target.MTR().View()) == 1 && target.MTR().Error() == "" }) {
			t.Errorf("pathOnly=%v: route not discovered after the failure cleared: error=%q hops=%d",
				pathOnly, target.MTR().Error(), len(target.MTR().View()))
		}
		eng.Stop()
	}
}
