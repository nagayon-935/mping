package mtr

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

type trackedSocket struct{ closed atomic.Int32 }

func (s *trackedSocket) Close() { s.closed.Add(1) }

type replacingProber struct {
	target  *stats.TargetStats
	cancel  context.CancelFunc
	sockets []*trackedSocket
}

func (p *replacingProber) OpenHopSocket(_ context.Context, _ string) (HopSocket, error) {
	s := &trackedSocket{}
	p.sockets = append(p.sockets, s)
	if len(p.sockets) == 2 {
		p.cancel()
	}
	return s, nil
}
func (p *replacingProber) ProbeHop(context.Context, HopSocket, string, int, int, time.Duration) (pinger.HopReply, error) {
	p.target.SetIP("127.0.0.2")
	return pinger.HopReply{Responded: true, ReachedDest: true, SrcIP: "127.0.0.1"}, nil
}
func (p *replacingProber) NextTraceID() int                 { return 10 }
func (p *replacingProber) ASNInfoFor(string) pinger.ASNInfo { return pinger.ASNInfo{} }
func TestReplacedMTRSocketClosedOnStop(t *testing.T) {
	ts := stats.NewTargetStats("example.invalid")
	ts.SetIP("127.0.0.1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := &replacingProber{target: ts, cancel: cancel}
	cfg := Config{MaxHops: 1, ProbeInterval: time.Hour, RediscoverEvery: time.Millisecond, HopTimeout: time.Millisecond}
	runTarget(ctx, p, ts, cfg, nil)
	if len(p.sockets) != 2 {
		t.Fatalf("expected replacement, got %d sockets", len(p.sockets))
	}
	for i, s := range p.sockets {
		if got := s.closed.Load(); got != 1 {
			t.Errorf("socket %d closed %d times, want once", i, got)
		}
	}
}

type cancelableOpenProber struct {
	fakeProber
	entered chan struct{}
}

func (p *cancelableOpenProber) OpenHopSocket(ctx context.Context, _ string) (HopSocket, error) {
	close(p.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestEngineStopCancelsSocketResolution(t *testing.T) {
	p := &cancelableOpenProber{entered: make(chan struct{})}
	engine := NewEngine(p, []*stats.TargetStats{stats.NewTargetStats("example.invalid")}, Config{})
	engine.Start()
	<-p.entered
	done := make(chan struct{})
	go func() { engine.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on DNS")
	}
}
