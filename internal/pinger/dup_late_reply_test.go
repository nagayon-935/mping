package pinger

// Tests for duplicate-reply (DUP) and late-arrival classification: replies
// that don't match any entry in `unacked` because that entry was already
// resolved (a second reply for an already-acked seq -> DUP) or already swept
// as a timeout (a reply that shows up after loss was recorded -> late
// arrival). Before this feature, both cases were silently discarded
// (TestCharacterization_RunWorker_MismatchedReplyDiscarded pins that this
// stays true for a seq runWorker never even sent).

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// TestRunWorker_DuplicateReply_NotDoubleCountedAsRecv pins that a second
// reply for a seq already recorded as a success is counted as a duplicate,
// not as a second Recv -- double-counting a DUP as Recv would understate the
// real loss rate, exactly the bug ping.c's `--nreceived` on a dup avoids.
func TestRunWorker_DuplicateReply_NotDoubleCountedAsRecv(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 0 // unlimited: keep the worker alive to receive the injected dup

	var logBuf strings.Builder
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	ch := make(chan Reply, 2)
	p.targetChans[id] = ch

	done := make(chan struct{})
	go func() {
		p.runWorker(target, id, 50*time.Millisecond, 200*time.Millisecond)
		close(done)
	}()

	// First reply resolves seq 1 normally.
	time.Sleep(5 * time.Millisecond)
	ch <- Reply{TTL: 64, Seq: 1}
	// Second reply for the same seq arrives shortly after: a genuine
	// duplicate delivery (routing loop / L2 duplication), not a new probe.
	time.Sleep(5 * time.Millisecond)
	ch <- Reply{TTL: 64, Seq: 1}

	time.Sleep(10 * time.Millisecond)
	p.Close()
	<-done

	view := target.GetView()
	if view.Recv != 1 {
		t.Fatalf("Recv = %d, want 1 (dup must not be double-counted as Recv)", view.Recv)
	}
	if view.Duplicates != 1 {
		t.Fatalf("Duplicates = %d, want 1", view.Duplicates)
	}
	if view.Loss != 0 {
		t.Fatalf("Loss = %d, want 0", view.Loss)
	}
	if !strings.Contains(logBuf.String(), ",DUP,") {
		t.Errorf("expected a DUP status line in the CSV log, got: %q", logBuf.String())
	}
}

// TestRunWorker_LateReply_DoesNotChangeLoss pins that a reply arriving after
// its probe was already swept as a timeout is counted as a late arrival, and
// that the loss already recorded for that probe is left standing (a "slow
// but reachable" target must stay distinguishable from a genuinely dropping
// one -- see ping.c's -W/nrcvtimeout).
func TestRunWorker_LateReply_DoesNotChangeLoss(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 0

	var logBuf strings.Builder
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	ch := make(chan Reply, 1)
	p.targetChans[id] = ch

	done := make(chan struct{})
	go func() {
		// Long interval so no second probe is sent before the assertions
		// below; short timeout so the sweep fires quickly and predictably.
		p.runWorker(target, id, 300*time.Millisecond, 15*time.Millisecond)
		close(done)
	}()

	// Let seq 1 time out (no reply injected before the 15ms timeout).
	time.Sleep(40 * time.Millisecond)
	// Now the reply shows up, long after the sweep already recorded a loss.
	ch <- Reply{TTL: 64, Seq: 1}

	time.Sleep(20 * time.Millisecond)
	p.Close()
	<-done

	view := target.GetView()
	if view.Loss != 1 {
		t.Fatalf("Loss = %d, want 1 (the timeout stands)", view.Loss)
	}
	if view.Recv != 0 {
		t.Fatalf("Recv = %d, want 0 (a late reply is not a Recv)", view.Recv)
	}
	if view.LateReplies != 1 {
		t.Fatalf("LateReplies = %d, want 1", view.LateReplies)
	}
	if !strings.Contains(logBuf.String(), ",LateReply,") {
		t.Errorf("expected a LateReply status line in the CSV log, got: %q", logBuf.String())
	}
}

// TestRecentSeqHistory_BoundedEviction pins that recentSeqHistory never
// grows past recentSeqHistoryCap entries, satisfying the "memory does not
// grow unbounded" requirement directly against the type runWorker uses,
// independent of timing.
func TestRecentSeqHistory_BoundedEviction(t *testing.T) {
	h := newRecentSeqHistory()
	const n = recentSeqHistoryCap * 3
	for i := 0; i < n; i++ {
		h.record(i%(seqMask+1), i, resolvedAcked, time.Now())
	}
	if got := h.order.Len(); got > recentSeqHistoryCap {
		t.Fatalf("recentSeqHistory grew to %d entries, want <= %d", got, recentSeqHistoryCap)
	}
	if got := len(h.index); got > recentSeqHistoryCap {
		t.Fatalf("recentSeqHistory index has %d entries, want <= %d", got, recentSeqHistoryCap)
	}
}

// TestRecentSeqHistory_WraparoundSafety pins the core safety property that
// keeps DUP/late classification correct across the 16-bit ICMP seq
// wraparound (see seqMask's doc): recentSeqHistoryCap must sit far enough
// below one full wraparound period (seqMask+1 = 65536) that, by the time a
// wire seq value is reused by a brand-new logical probe, this cache has long
// since evicted whatever it remembered about that wire seq's previous
// generation -- otherwise a reply for the *new* probe's generation could be
// misclassified using *old* generation history.
func TestRecentSeqHistory_WraparoundSafety(t *testing.T) {
	if recentSeqHistoryCap >= seqMask+1 {
		t.Fatalf("recentSeqHistoryCap (%d) must stay below one full 16-bit wraparound period (%d)", recentSeqHistoryCap, seqMask+1)
	}

	h := newRecentSeqHistory()
	const staleWireSeq = 42
	h.record(staleWireSeq, 42, resolvedAcked, time.Now())

	// Fill the cache with recentSeqHistoryCap other, distinct entries -- more
	// than enough to evict the stale one above, but nowhere near a full
	// 65536-wide wraparound cycle.
	for i := 0; i < recentSeqHistoryCap; i++ {
		wire := (i + 1000) % (seqMask + 1)
		if wire == staleWireSeq {
			continue
		}
		h.record(wire, i+1000, resolvedAcked, time.Now())
	}

	if _, ok := h.lookup(staleWireSeq); ok {
		t.Fatalf("stale entry for wire seq %d was not evicted after %d newer insertions; a reused wire seq could be misclassified using stale generation history", staleWireSeq, recentSeqHistoryCap)
	}
}

// TestRunWorker_UnknownSeq_StillSilentlyDiscarded guards against the DUP/
// late feature accidentally widening what counts as "recognized" history: a
// reply for a seq runWorker never sent and never resolved must remain
// silently discarded, exactly as
// TestCharacterization_RunWorker_MismatchedReplyDiscarded already pins for
// the pre-unacked-history behavior.
func TestRunWorker_UnknownSeq_StillSilentlyDiscarded(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	var logBuf strings.Builder
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	ch := make(chan Reply, 2)
	p.targetChans[id] = ch

	go func() {
		time.Sleep(5 * time.Millisecond)
		ch <- Reply{TTL: 1, Seq: 99} // never sent, never resolved
		ch <- Reply{TTL: 64, Seq: 1} // the real match
	}()

	p.runWorker(target, id, 10*time.Millisecond, 200*time.Millisecond)

	view := target.GetView()
	if view.Recv != 1 {
		t.Fatalf("Recv = %d, want 1", view.Recv)
	}
	if view.Duplicates != 0 {
		t.Fatalf("Duplicates = %d, want 0 (seq 99 was never resolved before)", view.Duplicates)
	}
	if view.LateReplies != 0 {
		t.Fatalf("LateReplies = %d, want 0 (seq 99 was never swept as a timeout)", view.LateReplies)
	}
}
