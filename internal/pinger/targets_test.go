package pinger

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

func liveTestPinger(t *testing.T) *Pinger {
	t.Helper()
	p := NewPingerWithOptions(nil, Options{IDs: &IDAllocator{}, ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.ParseIP("127.0.0.1")}, nil
	}})
	p.connV4 = &fakePacketConn{}
	p.interval = 20 * time.Millisecond
	p.probeTimeout = time.Second
	t.Cleanup(func() { p.Stop(); p.Wait(); p.Close() })
	return p
}

func awaitTarget(t *testing.T, target *stats.TargetStats, ready func(stats.TargetView) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready(target.GetView()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("target condition not met: %+v", target.GetView())
}

func echoReply(id, seq int) *icmp.Message {
	return &icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: &icmp.Echo{ID: id, Seq: seq}}
}

func TestLiveTargetRemovalRejectsOldRepliesAndPreservesSurvivor(t *testing.T) {
	p := liveTestPinger(t)
	p.interval = time.Hour // One outstanding probe per target, including zero-byte payload.
	p.Size = 0
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("other")
	for _, target := range []*stats.TargetStats{a, b} {
		if err := p.AddTarget(target, target.Host, nil); err != nil {
			t.Fatal(err)
		}
		awaitTarget(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
	}
	p.mapMu.RLock()
	old := p.workerStates[a].id
	survivor := p.workerStates[b]
	p.mapMu.RUnlock()
	p.handleEchoReply(echoReply(survivor.id, 1), 64, 0)
	awaitTarget(t, b, func(v stats.TargetView) bool { return v.Recv == 1 })
	p.RemoveTarget(a)
	if v := a.GetView(); v.Cancelled != 1 || v.Loss != 0 {
		t.Fatalf("deletion counted as loss: %+v", v)
	}
	readded := stats.NewTargetStats("same")
	if err := p.AddTarget(readded, "same", nil); err != nil {
		t.Fatal(err)
	}
	awaitTarget(t, readded, func(v stats.TargetView) bool { return v.Sent == 1 })
	p.mapMu.RLock()
	newID := p.workerStates[readded].id
	still := p.workerStates[b]
	p.mapMu.RUnlock()
	if newID == old || still != survivor {
		t.Fatal("worker or wire identity reused")
	}
	p.handleEchoReply(echoReply(old, 1), 64, 0)
	p.handleEchoReply(echoReply(newID, 1), 64, 0)
	awaitTarget(t, readded, func(v stats.TargetView) bool { return v.Recv == 1 })
	p.RemoveTarget(readded)
	if v := readded.GetView(); v.Recv != 1 || v.Duplicates != 0 {
		t.Fatalf("old reply leaked: %+v", v)
	}
	if v := b.GetView(); v.Sent != 1 || v.Recv != 1 || v.Cancelled != 0 {
		t.Fatalf("survivor changed: %+v", v)
	}
}

func TestLiveTargetCountBudgetAndCompletionCanRepeat(t *testing.T) {
	p := liveTestPinger(t)
	p.Count = 2
	p.probeTimeout = 5 * time.Millisecond
	a := stats.NewTargetStats("a")
	if err := p.AddTarget(a, "a", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.WorkerEvents():
	case <-time.After(time.Second):
		t.Fatal("initial completion missing")
	}
	b := stats.NewTargetStats("b")
	if err := p.AddTarget(b, "b", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.WorkerEvents():
	case <-time.After(time.Second):
		t.Fatal("completion after addition missing")
	}
	if a.GetView().Sent != 2 || b.GetView().Sent != 2 || !p.WorkersFinished() {
		t.Fatal("count budget reset or completion wrong")
	}
}

func TestRemoveTargetCancelsDNSWithoutRecordingFailure(t *testing.T) {
	p := liveTestPinger(t)
	entered := make(chan struct{})
	p.resolveWithContext = func(ctx context.Context, _, _ string) (*net.IPAddr, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	a := stats.NewTargetStats("slow")
	if err := p.AddTarget(a, "slow", nil); err != nil {
		t.Fatal(err)
	}
	<-entered
	done := make(chan struct{})
	go func() { p.RemoveTarget(a); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("target DNS was not cancelled")
	}
	if a.GetView().Loss != 0 {
		t.Fatal("cancelled DNS counted as failure")
	}
}

func TestSessionIDsStayDistinctAcrossRestartsAndRouteProbes(t *testing.T) {
	ids := &IDAllocator{}
	first, _ := ids.echoID()
	second, _ := ids.echoID()
	trace := ids.traceID()
	if first == second || trace < 32768 || first >= 32768 {
		t.Fatal("ID namespaces overlap")
	}
	ids.releaseTrace(trace)
	ids.echo = 32768
	if _, err := ids.echoID(); err == nil {
		t.Fatal("exhausted IDs reused")
	}
}
