package pinger

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func TestNewPingerDefaults(t *testing.T) {
	p := NewPinger(nil)
	if p == nil {
		t.Fatal("expected pinger instance")
	}
	if p.Size != 56 {
		t.Fatalf("default Size: got %d", p.Size)
	}
	if p.ResolveInterval <= 0 {
		t.Fatalf("expected positive ResolveInterval, got %v", p.ResolveInterval)
	}
	if p.resolveWithContext == nil || p.lookupTXTContext == nil || p.lookupAddrContext == nil || p.now == nil || p.listenPacket == nil {
		t.Fatal("expected default option funcs to be set")
	}
	if p.done == nil {
		t.Fatal("expected done channel")
	}
}

// TestNewPingerDefaultJitter checks the production ASN/PTR lookup jitter
// stays within its documented spread, so concurrent targets don't burst the
// resolver.
func TestNewPingerDefaultJitter(t *testing.T) {
	p := NewPinger(nil)
	for range 50 {
		if j := p.asnJitter(); j < 0 || j >= asnJitterMax {
			t.Fatalf("asnJitter = %v, want [0, %v)", j, asnJitterMax)
		}
		if j := p.ptrJitter(); j < 0 || j >= ptrJitterMax {
			t.Fatalf("ptrJitter = %v, want [0, %v)", j, ptrJitterMax)
		}
	}
}

func TestStart_SocketsFollowSourceFamily(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		wantV4, wantV6 bool
	}{
		{"no source opens both families", "", true, true},
		{"IPv4 source opens IPv4 only", "1.1.1.1", true, false},
		{"IPv6 source opens IPv6 only", "2001:db8::1", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{ListenPacket: listenWith(&fakeNetPacketConn{})})
			p.Source = tt.source
			if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err != nil {
				t.Fatalf("start: %v", err)
			}
			defer p.Close()

			if (p.connV4 != nil) != tt.wantV4 || (p.connV6 != nil) != tt.wantV6 {
				t.Fatalf("connV4 set=%v connV6 set=%v, want %v and %v", p.connV4 != nil, p.connV6 != nil, tt.wantV4, tt.wantV6)
			}
		})
	}
}

func TestStart_Errors(t *testing.T) {
	t.Run("listen failure", func(t *testing.T) {
		p := NewPingerWithOptions(nil, Options{
			ListenPacket: func(string, string) (net.PacketConn, error) { return nil, errors.New("listen failed") },
		})
		if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("echo IDs exhausted", func(t *testing.T) {
		ids := &IDAllocator{echo: 32768}
		p := NewPingerWithOptions([]*stats.TargetStats{stats.NewTargetStats("example.com")}, Options{
			IDs:          ids,
			ListenPacket: listenWith(&fakeNetPacketConn{}),
		})
		defer p.Close()
		if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err == nil {
			t.Fatal("expected ID exhaustion error")
		}
	})
}

// TestStart_WithTargetsRunsWorkers verifies that Start() with targets spawns
// workers that resolve and probe, and that Close/Wait unwinds them.
func TestStart_WithTargetsRunsWorkers(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ListenPacket:  listenWith(&fakeNetPacketConn{}),
		ResolveIPAddr: resolveTo("1.1.1.1"),
	})
	if err := p.Start(10*time.Millisecond, 50*time.Millisecond); err != nil {
		t.Fatalf("Start: %v", err)
	}
	eventually(t, 2*time.Second, "worker resolved the target", func() bool {
		return target.GetView().IP == "1.1.1.1"
	})
	p.Close()
	p.Wait()
}

// TestNewPingerWithOptions_ResolverIsUsedForLookups checks that a custom
// Options.Resolver backs both hostname resolution and TXT lookups (ASN),
// rather than net.DefaultResolver.
func TestNewPingerWithOptions_ResolverIsUsedForLookups(t *testing.T) {
	var dials atomic.Int32
	p := NewPingerWithOptions(nil, Options{Resolver: &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("mock: no dial")
		},
	}})

	if _, err := p.resolveIPAddrBounded("ip4", "nonexistent.invalid."); err == nil {
		t.Error("resolution through the failing resolver should fail")
	}
	if _, err := p.lookupTXTBounded("nonexistent.invalid."); err == nil {
		t.Error("TXT lookup through the failing resolver should fail")
	}
	if dials.Load() < 2 {
		t.Fatalf("custom resolver dialed %d times, want it used for both lookups", dials.Load())
	}
}

// TestStart_CountCompletionDoesNotWaitForReceiver checks that a finished
// Count run reports its workers as done while the receiver keeps running,
// and that nothing completes before outstanding probes have drained.
func TestStart_CountCompletionDoesNotWaitForReceiver(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		ListenPacket: func(string, string) (net.PacketConn, error) { return nil, errors.New("use injected socket") },
	})
	p.connV4 = &fakePacketConn{readDelay: time.Millisecond} // keeps the receiver polling without spinning
	p.Count = 2
	if err := p.Start(5*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(); p.Wait(); p.Close() })

	workersDone := make(chan struct{})
	go func() { p.WaitWorkers(); close(workersDone) }()
	select {
	case <-workersDone:
	case <-time.After(time.Second):
		t.Fatal("count completion blocked on receiver")
	}
	if v := target.GetView(); v.Sent != 2 || v.Loss != 2 {
		t.Fatalf("completion occurred before outstanding probes drained: sent=%d loss=%d", v.Sent, v.Loss)
	}

	allDone := make(chan struct{})
	go func() { p.Wait(); close(allDone) }()
	select {
	case <-allDone:
		t.Fatal("receiver stopped on worker completion")
	case <-time.After(20 * time.Millisecond):
	}
	p.Stop()
	select {
	case <-allDone:
	case <-time.After(time.Second):
		t.Fatal("receiver did not stop")
	}
}

func TestWaitBlocksUntilDone(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	p.wg.Add(1)
	var released atomic.Bool
	go func() {
		time.Sleep(10 * time.Millisecond) // give Wait time to block
		released.Store(true)
		p.wg.Done()
	}()
	p.Wait()
	if !released.Load() {
		t.Fatal("Wait returned before wg done")
	}
}

func TestPingerStop(t *testing.T) {
	t.Run("is idempotent", func(t *testing.T) {
		p := NewPinger(nil)
		p.Stop() // first Stop closes the channel without panic
		p.Stop() // second Stop is a no-op (already closed)
		select {
		case <-p.done:
		default:
			t.Fatal("done channel was not closed")
		}
	})

	t.Run("tolerates a pinger without a done channel", func(t *testing.T) {
		(&Pinger{}).Stop() // manually constructed fixtures must not panic
	})
}

// TestPingerStopConcurrent verifies Stop() is safe to call from multiple
// goroutines at once. The UI fires `go sup.stopAll()` from the 's' key
// handler (internal/ui/input_handler.go) while run()'s post-uiRun
// finishIteration path calls sup.stopAll() on the main goroutine, so two
// concurrent Stop() calls on the same *Pinger are reachable in practice.
// The previous select-guarded close was a check-then-act and panicked with
// "close of closed channel" when both goroutines passed the guard.
func TestPingerStopConcurrent(t *testing.T) {
	const iterations = 200
	const goroutines = 4

	for i := range iterations {
		p := NewPinger(nil)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range goroutines {
			wg.Go(func() {
				<-start
				p.Stop()
			})
		}
		close(start)
		wg.Wait()

		select {
		case <-p.done:
		default:
			t.Fatalf("iteration %d: done channel was not closed", i)
		}
	}
}

// TestResolveTargetUnblocksOnStop verifies a hung DNS resolution does not
// hold the shutdown path. resolveIPAddr takes no context and cannot be
// cancelled — with an unresponsive --dns-server the real resolver was
// measured blocking ~40s — so resolveTarget must bound it and bail out as
// soon as Stop() closes p.done. runTargetWorker calls resolveTarget on p.wg's
// goroutines, so anything it blocks on also blocks Pinger.Wait().
func TestResolveTargetUnblocksOnStop(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})

	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			close(entered)
			<-release // simulates a DNS server that never answers
			return nil, nil
		},
	})
	target := stats.NewTargetStats("example.com")

	returned := make(chan *net.IPAddr, 1)
	go func() { returned <- p.resolveTarget(target) }()

	<-entered // the resolve goroutine is inside its blocking call
	p.Stop()

	select {
	case addr := <-returned:
		if addr != nil {
			t.Fatalf("expected nil address on stop, got %v", addr)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("resolveTarget did not return within 1s of Stop()")
	}

	// Shutdown must not be recorded as a ping failure: OnFailure bumps Loss
	// and would pollute the exit summary printed by printExitSummary.
	if v := target.GetView(); v.Loss != 0 {
		t.Errorf("expected no loss recorded on shutdown, got Loss=%d LastError=%q", v.Loss, v.LastError)
	}
}
