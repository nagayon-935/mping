package pinger

import (
	"container/list"

	"github.com/nagayon-935/mping/internal/stats"
)

// Keep far fewer entries than the 16-bit wire sequence space so normal
// wraparound evicts a probe's history before its sequence is reused.
const recentSeqHistoryCap = 512

// resolutionKind records how a wire seq was most recently resolved, so a
// later reply for that same wire seq (once it's no longer in `unacked`) can
// be classified instead of just discarded.
type resolutionKind int

const (
	// resolvedAcked means the seq already got a reply -- a success or an
	// ICMP error, both of which remove the unacked entry the same way. A
	// further reply for the same wire seq is therefore a duplicate.
	resolvedAcked resolutionKind = iota
	// resolvedTimeout means the seq's unacked entry aged out and was swept
	// as a loss. A reply arriving now is a late arrival.
	resolvedTimeout
)

// recentEntry is one record in recentSeqHistory: how wireSeq was resolved,
// and the statistics generation it belongs to.
type recentEntry struct {
	wireSeq int
	kind    resolutionKind
	stats   stats.Probe
}

// recentSeqHistory is a small, size-bounded FIFO cache mapping a
// just-resolved wire seq to how it was resolved. runTargetWorker consults it only
// when a reply doesn't match anything in `unacked`, to tell a duplicate or a
// late arrival apart from a genuinely unrecognized/bogus seq (which is
// silently discarded).
//
// Capacity, not time, bounds it (see recentSeqHistoryCap's doc for why a
// bounded size rather than a time window is what keeps this safe across the
// 16-bit wire seq wraparound). It is owned exclusively by runTargetWorker's own
// goroutine, exactly like `unacked`, so it needs no mutex.
type recentSeqHistory struct {
	order *list.List            // front = oldest, back = newest
	index map[int]*list.Element // wireSeq -> its node in order
}

func newRecentSeqHistory() *recentSeqHistory {
	return &recentSeqHistory{
		order: list.New(),
		index: make(map[int]*list.Element),
	}
}

// record notes that wireSeq was just resolved as kind, evicting the oldest
// entry once the cache is over recentSeqHistoryCap. Re-recording an
// already-present wireSeq (not expected in normal operation -- a given wire
// seq is only resolved once per generation -- but handled safely) replaces
// its entry and moves it to the back as the newest.
func (h *recentSeqHistory) record(wireSeq int, kind resolutionKind, probe stats.Probe) {
	if el, ok := h.index[wireSeq]; ok {
		h.order.Remove(el)
	}
	el := h.order.PushBack(recentEntry{wireSeq: wireSeq, kind: kind, stats: probe})
	h.index[wireSeq] = el

	for h.order.Len() > recentSeqHistoryCap {
		oldest := h.order.Front()
		h.order.Remove(oldest)
		entry := oldest.Value.(recentEntry)
		// Only clear the index if it still points at this exact node: the
		// replace-and-move-to-back path above can leave a wireSeq's index
		// entry pointing at a newer node by the time an older, since
		// re-recorded node reaches the front.
		if h.index[entry.wireSeq] == oldest {
			delete(h.index, entry.wireSeq)
		}
	}
}

// lookup returns the recorded entry for wireSeq, if any.
func (h *recentSeqHistory) lookup(wireSeq int) (recentEntry, bool) {
	el, ok := h.index[wireSeq]
	if !ok {
		return recentEntry{}, false
	}
	return el.Value.(recentEntry), true
}
