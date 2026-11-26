package pinger

import "github.com/nagayon-935/mping/internal/stats"

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

type recentNode struct {
	entry recentEntry
	prev  int
	next  int
}

// recentSeqHistory is a small, size-bounded FIFO cache mapping a
// just-resolved wire seq to how it was resolved. runTargetWorker consults it only
// when a reply doesn't match anything in `unacked`, to tell a duplicate or a
// late arrival apart from a genuinely unrecognized/bogus seq (which is
// silently discarded).
//
// Backed by a fixed-size doubly-linked array so steady-state probes allocate
// zero heap nodes per settlement. Owned exclusively by runTargetWorker's own
// goroutine, exactly like `unacked`, so it needs no mutex.
type recentSeqHistory struct {
	nodes [recentSeqHistoryCap]recentNode
	head  int         // oldest slot, -1 when empty
	tail  int         // newest slot, -1 when empty
	free  int         // next unused slot while count < recentSeqHistoryCap
	count int         // active entries
	index map[int]int // wireSeq -> slot in nodes
}

func newRecentSeqHistory() *recentSeqHistory {
	return &recentSeqHistory{
		head:  -1,
		tail:  -1,
		index: make(map[int]int),
	}
}

func (h *recentSeqHistory) len() int { return h.count }

func (h *recentSeqHistory) newest() (recentEntry, bool) {
	if h.tail < 0 {
		return recentEntry{}, false
	}
	return h.nodes[h.tail].entry, true
}

// record notes that wireSeq was just resolved as kind, evicting the oldest
// entry once the cache reaches recentSeqHistoryCap. Re-recording an
// already-present wireSeq replaces its entry and moves it to the back as the
// newest without growing the cache.
func (h *recentSeqHistory) record(wireSeq int, kind resolutionKind, probe stats.Probe) {
	entry := recentEntry{wireSeq: wireSeq, kind: kind, stats: probe}
	if slot, ok := h.index[wireSeq]; ok {
		h.nodes[slot].entry = entry
		if slot != h.tail {
			prev, next := h.nodes[slot].prev, h.nodes[slot].next
			if prev >= 0 {
				h.nodes[prev].next = next
			} else {
				h.head = next
			}
			if next >= 0 {
				h.nodes[next].prev = prev
			}
			h.nodes[slot].prev = h.tail
			h.nodes[slot].next = -1
			h.nodes[h.tail].next = slot
			h.tail = slot
		}
		return
	}

	var slot int
	if h.count < recentSeqHistoryCap {
		slot = h.free
		h.free++
		h.count++
	} else {
		slot = h.head
		delete(h.index, h.nodes[slot].entry.wireSeq)
		next := h.nodes[slot].next
		if next >= 0 {
			h.nodes[next].prev = -1
			h.head = next
		} else {
			h.head = -1
			h.tail = -1
		}
	}

	h.nodes[slot] = recentNode{entry: entry, prev: h.tail, next: -1}
	if h.tail >= 0 {
		h.nodes[h.tail].next = slot
	} else {
		h.head = slot
	}
	h.tail = slot
	h.index[wireSeq] = slot
}

// lookup returns the recorded entry for wireSeq, if any.
func (h *recentSeqHistory) lookup(wireSeq int) (recentEntry, bool) {
	slot, ok := h.index[wireSeq]
	if !ok {
		return recentEntry{}, false
	}
	return h.nodes[slot].entry, true
}
