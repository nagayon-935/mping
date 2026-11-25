package mtr

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

type ttlTrackingProber struct {
	*fakeProber
	mu   sync.Mutex
	ttls []int
}

func (p *ttlTrackingProber) ProbeHop(ctx context.Context, sock HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	p.mu.Lock()
	p.ttls = append(p.ttls, ttl)
	p.mu.Unlock()
	return p.fakeProber.ProbeHop(ctx, sock, dest, ttl, traceID, timeout)
}

func (p *ttlTrackingProber) expectTTLs(t *testing.T, lastHop int, checkReachability bool) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	want := make([]int, 0, lastHop+1)
	for ttl := 1; ttl <= lastHop; ttl++ {
		want = append(want, ttl)
	}
	if checkReachability {
		want = append(want, 255)
	}
	slices.Sort(p.ttls)
	if !slices.Equal(p.ttls, want) {
		t.Fatalf("sent TTLs = %v, want %v", p.ttls, want)
	}
	p.ttls = nil
}

func TestEngine_ReachableLongRoute(t *testing.T) {
	for _, mode := range []struct {
		name     string
		pathOnly bool
	}{
		{"traceroute", true},
		{"MTR", false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const dest = "192.0.2.1"
				target := newTestTarget(dest)
				echo := pinger.HopReply{SrcIP: dest, Responded: true, ReachedDest: true}
				longRoute := map[int]pinger.HopReply{25: echo, 255: echo}
				prober := &ttlTrackingProber{fakeProber: newFakeProber(longRoute)}
				engine := NewEngine(prober, []*stats.TargetStats{target}, Config{
					PathOnly: mode.pathOnly, ProbeInterval: time.Second, RediscoverEvery: time.Hour,
				})
				engine.Start()
				defer engine.Stop()
				synctest.Wait()
				prober.expectTTLs(t, 30, true)
				if hops := target.MTR().View(); len(hops) != 25 || hops[24].IP != dest {
					t.Fatalf("long route has %d hops, want 25 with destination at hop 25", len(hops))
				}

				time.Sleep(time.Second)
				synctest.Wait()
				if mode.pathOnly {
					prober.expectTTLs(t, 0, false)
					return
				}
				prober.expectTTLs(t, 25, true)
				if got := target.MTR().View()[24].Sent; got != 1 {
					t.Fatalf("destination hop has %d samples after one round, want 1", got)
				}

				prober.setReplies(nil)
				time.Sleep(time.Second)
				synctest.Wait()
				prober.expectTTLs(t, 20, true)
				if got := target.MTR().View()[24].Sent; got != 1 {
					t.Fatalf("destination hop has %d samples after it went silent, want 1", got)
				}

				prober.setReplies(longRoute)
				time.Sleep(time.Second)
				synctest.Wait()
				prober.expectTTLs(t, 25, true)
				if got := target.MTR().View()[24].Sent; got != 2 {
					t.Fatalf("destination hop has %d samples after recovery, want 2", got)
				}
			})
		})
	}
}

func TestEngine_ReachabilityEchoPreservesPartialRoute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const dest = "192.0.2.1"
		target := newTestTarget(dest)
		prober := &ttlTrackingProber{fakeProber: newFakeProber(map[int]pinger.HopReply{
			1:   {SrcIP: "198.51.100.1", Responded: true, UnreachableReason: "Destination Host Unreachable"},
			23:  {SrcIP: "198.51.100.23", Responded: true},
			255: {SrcIP: dest, Responded: true, ReachedDest: true},
		})}
		engine := NewEngine(prober, []*stats.TargetStats{target}, Config{PathOnly: true})
		engine.Start()
		defer engine.Stop()
		synctest.Wait()
		prober.expectTTLs(t, 30, true)
		hops := target.MTR().View()
		if err := target.MTR().Error(); err != "" || len(hops) != 30 || hops[0].IP != "" ||
			hops[22].IP != "198.51.100.23" || hops[29].IP != "" {
			t.Fatalf("want a 30-hop partial route with only hop 23 visible, got %v, error %q", hops, err)
		}
	})
}
