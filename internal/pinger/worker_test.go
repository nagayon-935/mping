package pinger

// Behavior tests for runTargetWorker (via the runWorker test helper): stats
// transitions (Sent/Recv/Loss/LastRTT/LastTTL/LastError), the CSV log
// format/status strings/seq numbering, Count's send-then-drain semantics,
// prompt termination on Stop()/Close(), and silent discarding of a reply
// that doesn't match any outstanding probe. These were originally written as
// characterization tests ahead of the async probe loop refactor and must keep
// passing unmodified across any further rewrite of the loop.

import (
	"errors"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// workerRig wires one target to a Pinger with fake sockets so runWorker can
// be driven deterministically.
type workerRig struct {
	t      *testing.T
	target *stats.TargetStats
	p      *Pinger
	conn   *fakePacketConn
	id     int
	ch     chan Reply

	// scripted maps "the n-th probe written" to the replies delivered the
	// moment it hits the wire. Only the worker goroutine touches writes/scripted.
	scripted map[int][]Reply
	writes   int
}

func newWorkerRig(t *testing.T, count int) *workerRig {
	t.Helper()
	r := &workerRig{t: t, target: stats.NewTargetStats("example.com"), conn: &fakePacketConn{}, scripted: map[int][]Reply{}}
	r.p = NewPingerWithOptions([]*stats.TargetStats{r.target}, Options{ResolveIPAddr: resolveTo("1.1.1.1")})
	r.p.connV4 = r.conn
	r.p.Count = count
	r.id = r.p.baseID & 0xffff
	r.ch = make(chan Reply, 16)
	r.p.targetChans[r.id] = r.ch
	r.conn.onWrite = func([]byte, net.Addr) error {
		r.writes++
		for _, rep := range r.scripted[r.writes] {
			r.ch <- rep
		}
		return nil
	}
	return r
}

// replyOnWrite delivers replies as soon as the n-th probe is written. The
// worker is a single goroutine that records a probe as outstanding before it
// next services its reply channel, so a reply queued from inside the write
// can never race the bookkeeping the way a sleeping goroutine could.
func (r *workerRig) replyOnWrite(n int, replies ...Reply) {
	r.scripted[n] = append(r.scripted[n], replies...)
}

func (r *workerRig) run(interval, timeout time.Duration) {
	r.p.runWorker(r.target, r.id, interval, timeout)
}

// runAsync starts the worker in the background; done closes when it returns.
func (r *workerRig) runAsync(interval, timeout time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		r.run(interval, timeout)
		close(done)
	}()
	return done
}

// stopAndJoin closes the pinger and waits for the worker to return.
func (r *workerRig) stopAndJoin(done <-chan struct{}) {
	r.t.Helper()
	r.p.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		r.t.Fatal("runWorker did not stop after Close()")
	}
}

// TestRunWorker_SingleProbeOutcomes pins, for a one-probe run, the stats
// transitions for each way a probe can end.
func TestRunWorker_SingleProbeOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(r *workerRig)
		timeout  time.Duration
		wantSent int
		wantRecv int
		wantLoss int
		wantErr  string // TargetView.LastError
	}{
		{
			name:     "reply",
			setup:    func(r *workerRig) { r.replyOnWrite(1, Reply{TTL: 55, Seq: 1}) },
			timeout:  200 * time.Millisecond,
			wantSent: 1, wantRecv: 1,
		},
		{
			name:     "timeout",
			setup:    func(*workerRig) {},
			timeout:  20 * time.Millisecond,
			wantSent: 1, wantLoss: 1,
			wantErr: "Timeout",
		},
		{
			name:     "ICMP error reply",
			setup:    func(r *workerRig) { r.replyOnWrite(1, Reply{Seq: 1, Err: "Time Exceeded"}) },
			timeout:  200 * time.Millisecond,
			wantSent: 1, wantLoss: 1,
			wantErr: "Time Exceeded",
		},
		{
			// IncSent only fires after a successful WriteTo, so a failed
			// write is a loss but not a sent probe.
			name:     "send error",
			setup:    func(r *workerRig) { r.conn.writeErr = errors.New("send failed") },
			timeout:  200 * time.Millisecond,
			wantLoss: 1,
			wantErr:  "send failed",
		},
		{
			name:     "no socket for the address family",
			setup:    func(r *workerRig) { r.p.connV4 = nil },
			timeout:  200 * time.Millisecond,
			wantLoss: 1,
			wantErr:  "No IPv4 Conn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newWorkerRig(t, 1)
			tt.setup(r)

			done := r.runAsync(5*time.Millisecond, tt.timeout)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("runWorker did not finish a Count=1 run on its own")
			}

			v := r.target.GetView()
			if v.Sent != tt.wantSent || v.Recv != tt.wantRecv || v.Loss != tt.wantLoss || v.LastError != tt.wantErr {
				t.Fatalf("Sent=%d Recv=%d Loss=%d LastError=%q, want %d %d %d %q",
					v.Sent, v.Recv, v.Loss, v.LastError, tt.wantSent, tt.wantRecv, tt.wantLoss, tt.wantErr)
			}
			if tt.wantRecv == 1 {
				if v.LastTTL != 55 || v.LastRTT <= 0 {
					t.Fatalf("LastTTL=%d LastRTT=%v, want 55 and a positive RTT", v.LastTTL, v.LastRTT)
				}
			}
		})
	}
}

// TestRunWorker_MismatchedReplyDiscarded pins the rule that a reply whose Seq
// doesn't match any outstanding probe is silently discarded: it must not be
// counted as Recv, Loss, duplicate or late arrival, and the
// eventual correctly-matching reply must still be recorded normally. (Only
// seqs the worker actually resolved earlier are classified as DUP/LateReply;
// see dup_late_reply_test.go.)
func TestRunWorker_MismatchedReplyDiscarded(t *testing.T) {
	r := newWorkerRig(t, 1)
	r.replyOnWrite(1,
		Reply{TTL: 1, Seq: 99}, // never sent, never resolved: must be discarded
		Reply{TTL: 64, Seq: 1}, // the real match
	)

	r.run(10*time.Millisecond, 200*time.Millisecond)

	v := r.target.GetView()
	if v.Recv != 1 || v.Loss != 0 || v.Duplicates != 0 || v.LateReplies != 0 {
		t.Fatalf("Recv=%d Loss=%d Duplicates=%d LateReplies=%d, want 1 0 0 0 (mismatched reply must not be counted)",
			v.Recv, v.Loss, v.Duplicates, v.LateReplies)
	}
	if v.LastTTL != 64 {
		t.Fatalf("LastTTL = %d, want 64 (from the matching reply, not the discarded one)", v.LastTTL)
	}
}

// TestRunWorker_CountSendsThenDrains pins the seq numbering (1, 2, 3, ...)
// across a multi-probe Count run, all replies succeeding: each reply carries
// the seq it answers, so a numbering slip would leave probes unmatched.
func TestRunWorker_CountSendsThenDrains(t *testing.T) {
	r := newWorkerRig(t, 3)
	for n := 1; n <= 3; n++ {
		r.replyOnWrite(n, Reply{TTL: 64, Seq: n})
	}

	r.run(10*time.Millisecond, 200*time.Millisecond)

	if v := r.target.GetView(); v.Sent != 3 || v.Recv != 3 {
		t.Fatalf("Sent=%d Recv=%d, want 3 and 3", v.Sent, v.Recv)
	}
}

// TestRunWorker_ZeroResolveIntervalStillRuns covers the ResolveInterval <= 0
// branch, which falls back to a 60s re-resolve period instead of panicking in
// time.NewTicker.
func TestRunWorker_ZeroResolveIntervalStillRuns(t *testing.T) {
	r := newWorkerRig(t, 1)
	r.p.ResolveInterval = 0
	r.replyOnWrite(1, Reply{TTL: 64, Seq: 1})

	r.run(10*time.Millisecond, 200*time.Millisecond)

	if v := r.target.GetView(); v.Recv != 1 {
		t.Fatalf("Recv = %d, want 1", v.Recv)
	}
}

// TestRunWorker_ReResolvesOnDNSTicker checks the worker re-resolves its
// target every ResolveInterval while it keeps probing.
func TestRunWorker_ReResolvesOnDNSTicker(t *testing.T) {
	var calls atomic.Int32
	r := newWorkerRig(t, 2)
	r.p.resolveWithContext = nil
	r.p.resolveIPAddr = func(string, string) (*net.IPAddr, error) {
		calls.Add(1)
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	r.p.ResolveInterval = 5 * time.Millisecond // far shorter than the send interval
	r.replyOnWrite(1, Reply{TTL: 64, Seq: 1})
	r.replyOnWrite(2, Reply{TTL: 64, Seq: 2})

	r.run(50*time.Millisecond, 200*time.Millisecond)

	if v := r.target.GetView(); v.Recv != 2 {
		t.Fatalf("Recv = %d, want 2", v.Recv)
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("resolver called %d times, want the initial lookup plus at least one ticker re-resolve", got)
	}
}

// TestRunWorker_RetriesResolutionUntilTargetResolves covers a target whose
// initial DNS lookup fails: the worker retries on each send tick and only
// starts probing once resolution succeeds.
func TestRunWorker_RetriesResolutionUntilTargetResolves(t *testing.T) {
	var calls atomic.Int32
	r := newWorkerRig(t, 1)
	r.p.resolveWithContext = nil
	r.p.resolveIPAddr = func(string, string) (*net.IPAddr, error) {
		if calls.Add(1) <= 2 { // the worker's initial lookup and its first retry fail
			return nil, errors.New("dns failure")
		}
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	r.replyOnWrite(1, Reply{TTL: 64, Seq: 1})

	r.run(5*time.Millisecond, 200*time.Millisecond)

	v := r.target.GetView()
	if v.Sent != 1 || v.Recv != 1 {
		t.Fatalf("Sent=%d Recv=%d, want 1 and 1 once resolution succeeds", v.Sent, v.Recv)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("resolver called %d times, want 3 (two failures, then success)", got)
	}
}

// TestRunWorker_SeqWraparound proves the ICMP echo seq wraparound fix: once
// the logical seq counter crosses 65535, the wire-level reply comes back
// with Seq wrapped into [0, 65535] (golang.org/x/net/icmp.ParseMessage never
// returns a value outside that range), and it must still be matched as a
// success — not dropped as a mismatch that degrades into a permanent
// timeout/loss for every subsequent probe.
func TestRunWorker_SeqWraparound(t *testing.T) {
	r := newWorkerRig(t, 0) // unlimited; the test stops the worker explicitly
	r.p.initialSeq = 65535  // seq++ makes the first logical seq 65536
	// Simulates the real wire reply to logical seq 65536: the receiver
	// parses Seq as a wrapped uint16, i.e. 0.
	r.replyOnWrite(1, Reply{TTL: 64, Seq: 0})

	done := r.runAsync(time.Hour, 200*time.Millisecond)
	awaitTarget(t, r.target, func(v stats.TargetView) bool { return v.Recv == 1 })
	r.stopAndJoin(done)

	v := r.target.GetView()
	if v.Recv != 1 || v.Loss != 0 || v.LastTTL != 64 {
		t.Fatalf("Recv=%d Loss=%d LastTTL=%d, want 1 0 64 (wrapped-seq reply should match as success, not be lost to a permanent mismatch)",
			v.Recv, v.Loss, v.LastTTL)
	}
}

// TestRunWorker_StopEndsUnlimitedRunWithoutLeaks pins that Stop()/Close()
// terminates an unlimited (Count=0) worker promptly, with no leaked
// goroutines (ticker/timer goroutines included).
func TestRunWorker_StopEndsUnlimitedRunWithoutLeaks(t *testing.T) {
	r := newWorkerRig(t, 0)
	before := runtime.NumGoroutine()

	done := r.runAsync(5*time.Millisecond, 200*time.Millisecond)
	awaitTarget(t, r.target, func(v stats.TargetView) bool { return v.Sent >= 2 })
	r.stopAndJoin(done)

	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before {
		t.Errorf("goroutine leak after runWorker stopped: before=%d after=%d", before, after)
	}
}
