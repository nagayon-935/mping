package mtr

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestEngine_PathOnlyDiscoversWithoutProbing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := newTestTarget("8.8.8.8")
		prober := newFakeProber(map[int]pinger.HopReply{
			1: {SrcIP: "10.0.0.1", Responded: true},
			2: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
		})
		prober.ptrs = map[string]string{"10.0.0.1": "gw.example"}
		cfg := Config{PathOnly: true, ProbeInterval: time.Second, RediscoverEvery: time.Hour, MaxHops: 4}
		eng := NewEngine(prober, []*stats.TargetStats{target}, cfg)
		eng.Start()
		defer eng.Stop()
		time.Sleep(5 * time.Second) // Cross several potential probe ticks.
		synctest.Wait()

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
			t.Errorf("hops = %+v, want PTR gw.example on hop 1 and none on hop 2", hops)
		}
	})
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
		t.Cleanup(eng.Stop)

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

func TestEngine_SilentDestinationUsesTwentyHopLimit(t *testing.T) {
	for _, tt := range []struct {
		name     string
		pathOnly bool
	}{
		{name: "traceroute", pathOnly: true},
		{name: "MTR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				target := newTestTarget("8.8.8.8")
				prober := &ttlTrackingProber{fakeProber: newFakeProber(map[int]pinger.HopReply{
					1: {SrcIP: "10.0.0.1", Responded: true},
				})}
				engine := NewEngine(prober, []*stats.TargetStats{target}, Config{PathOnly: tt.pathOnly, ProbeInterval: time.Second})
				engine.Start()
				defer engine.Stop()
				synctest.Wait()

				hops := target.MTR().View()
				if len(hops) != 20 || hops[0].IP != "10.0.0.1" || hops[19].IP != "" {
					t.Fatalf("partial path has %d hops: %+v", len(hops), hops)
				}
				prober.expectTTLs(t, 20, true)
				if err := target.MTR().Error(); err != "" {
					t.Errorf("silent destination reported route error %q", err)
				}

				time.Sleep(time.Second)
				synctest.Wait()
				if tt.pathOnly {
					prober.expectTTLs(t, 0, false)
				} else {
					prober.expectTTLs(t, 20, false)
				}
			})
		})
	}
}

func TestEngine_PathOnlyUnreachableDiscoveryRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := newTestTarget("8.8.8.8")
		prober := &ttlTrackingProber{fakeProber: newFakeProber(map[int]pinger.HopReply{
			255: {SrcIP: "10.0.0.1", Responded: true, UnreachableReason: "Destination Host Unreachable"},
		})}
		engine := NewEngine(prober, []*stats.TargetStats{target}, Config{PathOnly: true, RediscoverEvery: time.Minute})
		engine.Start()
		defer engine.Stop()
		synctest.Wait()

		const wantError = "8.8.8.8: Destination Host Unreachable (reported by 10.0.0.1)"
		if got := target.MTR().Error(); got != wantError || len(target.MTR().View()) != 0 {
			t.Fatalf("failed discovery published hops=%+v, error=%q", target.MTR().View(), got)
		}
		prober.expectTTLs(t, 20, true)

		// A destination echo reply proves the target was reached even when
		// another probe in the round received an ICMP rejection.
		prober.setReplies(map[int]pinger.HopReply{
			1: {SrcIP: "10.0.0.1", Responded: true, UnreachableReason: "Destination Host Unreachable"},
			2: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
			3: {SrcIP: "8.8.8.8", Responded: true, ReachedDest: true},
		})
		time.Sleep(time.Minute)
		synctest.Wait()

		hops := target.MTR().View()
		if err := target.MTR().Error(); err != "" || len(hops) != 2 || hops[0].IP != "" || hops[1].IP != "8.8.8.8" {
			t.Fatalf("recovered discovery published hops=%+v, error=%q", hops, err)
		}
		prober.expectTTLs(t, 20, false)
	})
}
