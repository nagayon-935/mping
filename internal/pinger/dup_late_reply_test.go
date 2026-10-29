package pinger

// Tests for duplicate-reply (DUP) and late-arrival classification: replies
// that don't match any entry in `unacked` because that entry was already
// resolved (a second reply for an already-acked seq -> DUP) or already swept
// as a timeout (a reply that shows up after loss was recorded -> late
// arrival). Before this feature, both cases were silently discarded
// (TestRunWorker_MismatchedReplyDiscarded pins that this stays true for a
// seq the worker never even sent).

import (
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
	r := newWorkerRig(t, 0) // unlimited: keep the worker alive to receive the dup
	// The first reply resolves seq 1 normally; the second, for the same seq,
	// is a genuine duplicate delivery (routing loop / L2 duplication), not a
	// new probe.
	r.replyOnWrite(1, Reply{TTL: 64, Seq: 1}, Reply{TTL: 64, Seq: 1})

	done := r.runAsync(time.Hour, 200*time.Millisecond)
	awaitTarget(t, r.target, func(v stats.TargetView) bool { return v.Duplicates == 1 })
	r.stopAndJoin(done)

	view := r.target.GetView()
	if view.Recv != 1 || view.Duplicates != 1 || view.Loss != 0 {
		t.Fatalf("Recv=%d Duplicates=%d Loss=%d, want 1 1 0 (dup must not be double-counted as Recv)",
			view.Recv, view.Duplicates, view.Loss)
	}
	if !strings.Contains(r.logs.String(), ",DUP,") {
		t.Errorf("expected a DUP status line in the CSV log, got: %q", r.logs.String())
	}
}

// TestRunWorker_LateReply_DoesNotChangeLoss pins that a reply arriving after
// its probe was already swept as a timeout is counted as a late arrival, and
// that the loss already recorded for that probe is left standing (a "slow
// but reachable" target must stay distinguishable from a genuinely dropping
// one -- see ping.c's -W/nrcvtimeout).
func TestRunWorker_LateReply_DoesNotChangeLoss(t *testing.T) {
	r := newWorkerRig(t, 0)

	// Long interval so no second probe is sent before the assertions below;
	// short timeout so the sweep fires quickly and predictably.
	done := r.runAsync(time.Hour, 15*time.Millisecond)
	// Seq 1 times out (no reply is injected before the sweep)...
	awaitTarget(t, r.target, func(v stats.TargetView) bool { return v.Loss == 1 })
	// ...and only then does its reply show up.
	r.ch <- Reply{TTL: 64, Seq: 1}
	awaitTarget(t, r.target, func(v stats.TargetView) bool { return v.LateReplies == 1 })
	r.stopAndJoin(done)

	view := r.target.GetView()
	if view.Loss != 1 || view.Recv != 0 || view.LateReplies != 1 {
		t.Fatalf("Loss=%d Recv=%d LateReplies=%d, want 1 0 1 (the timeout stands; a late reply is not a Recv)",
			view.Loss, view.Recv, view.LateReplies)
	}
	if !strings.Contains(r.logs.String(), ",LateReply,") {
		t.Errorf("expected a LateReply status line in the CSV log, got: %q", r.logs.String())
	}
}

// TestRecentSeqHistory_BoundedEviction pins that recentSeqHistory never
// grows past recentSeqHistoryCap entries, satisfying the "memory does not
// grow unbounded" requirement directly against the type runWorker uses,
// independent of timing.
func TestRecentSeqHistory_BoundedEviction(t *testing.T) {
	h := newRecentSeqHistory()
	const n = recentSeqHistoryCap * 3
	for i := range n {
		h.record(i%(seqMask+1), i, resolvedAcked, time.Now(), stats.Probe{})
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
	h.record(staleWireSeq, 42, resolvedAcked, time.Now(), stats.Probe{})

	// Fill the cache with recentSeqHistoryCap other, distinct entries -- more
	// than enough to evict the stale one above, but nowhere near a full
	// 65536-wide wraparound cycle.
	for i := range recentSeqHistoryCap {
		wire := (i + 1000) % (seqMask + 1)
		if wire == staleWireSeq {
			continue
		}
		h.record(wire, i+1000, resolvedAcked, time.Now(), stats.Probe{})
	}

	if _, ok := h.lookup(staleWireSeq); ok {
		t.Fatalf("stale entry for wire seq %d was not evicted after %d newer insertions; a reused wire seq could be misclassified using stale generation history", staleWireSeq, recentSeqHistoryCap)
	}
}

// TestRecentSeqHistory_ReRecordReplacesAndRefreshesEntry pins that recording
// an already-present wire seq replaces its entry and moves it to the back as
// the newest, rather than leaving a stale duplicate node behind.
func TestRecentSeqHistory_ReRecordReplacesAndRefreshesEntry(t *testing.T) {
	h := newRecentSeqHistory()
	now := time.Now()
	h.record(7, 7, resolvedAcked, now, stats.Probe{})
	h.record(9, 9, resolvedAcked, now, stats.Probe{})

	h.record(7, 71, resolvedTimeout, now, stats.Probe{})

	if got := h.order.Len(); got != 2 {
		t.Fatalf("history has %d entries, want 2 (re-recording must not add a duplicate)", got)
	}
	entry, ok := h.lookup(7)
	if !ok || entry.logicalSeq != 71 || entry.kind != resolvedTimeout {
		t.Fatalf("lookup(7) = %+v, %v; want the replaced timeout entry for logical seq 71", entry, ok)
	}
	if newest := h.order.Back().Value.(recentEntry); newest.wireSeq != 7 {
		t.Fatalf("newest entry is wire seq %d, want 7 (re-recorded entry moves to the back)", newest.wireSeq)
	}
}
