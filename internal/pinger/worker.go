package pinger

import (
	"container/list"
	"context"
	"net"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// getWriteFunc returns the appropriate ICMP message type and write function
// for the given destination address.
func (p *Pinger) getWriteFunc(dstAddr *net.IPAddr) (icmp.Type, func([]byte, net.Addr) (int, error), string) {
	if dstAddr.IP.To4() != nil {
		if p.connV4 != nil {
			return ipv4.ICMPTypeEcho, func(b []byte, dst net.Addr) (int, error) {
				return p.connV4.WriteTo(b, nil, dst)
			}, ""
		}
		return nil, nil, "No IPv4 Conn"
	}
	if p.connV6 != nil {
		return ipv6.ICMPTypeEchoRequest, func(b []byte, dst net.Addr) (int, error) {
			return p.connV6.WriteTo(b, nil, dst)
		}, ""
	}
	return nil, nil, "No IPv6 Conn"
}

func (p *Pinger) sendProbeWithStats(t *stats.TargetStats, probe stats.Probe, id, seq int, payload []byte, dstAddr *net.IPAddr) (time.Time, bool) {
	msgType, writeFunc, errStr := p.getWriteFunc(dstAddr)
	if writeFunc == nil {
		probe.OnFailure(errStr)
		return time.Time{}, false
	}

	// start is captured before the payload is marshaled (rather than right
	// before the write) because embedSendTimestamp needs it embedded in
	// `payload` first. This is the same instant used for both
	// the embedded timestamp and the pendingProbe fallback in runTargetWorker, so
	// the two stay consistent with each other.
	start := time.Now()
	embedSendTimestamp(payload, start)

	msg := icmp.Message{
		Type: msgType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  seq & seqMask,
			Data: payload,
		},
	}
	b, err := marshalProbe(&msg)
	if err != nil {
		return time.Time{}, false
	}

	_, err = writeFunc(b, dstAddr)
	if err != nil {
		errMsg := p.applyLastErrSource(err.Error())
		probe.OnFailure(errMsg)
		return time.Time{}, false
	}

	probe.IncSent()
	return start, true
}

// pendingProbe tracks one in-flight probe: the time it was sent (for RTT and
// expiry) and the statistics generation it belongs to.
type pendingProbe struct {
	start time.Time
	stats stats.Probe
}

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

// nextExpiry returns the earliest deadline among unacked's entries, and
// whether unacked is non-empty. Because runTargetWorker only ever inserts entries
// with monotonically increasing start times and a single, constant timeout,
// the earliest deadline is a plain O(n) scan; n is bounded by the number of
// probes currently in flight (interval/timeout ratio), which stays small in
// practice.
func nextExpiry(unacked map[int]pendingProbe, timeout time.Duration) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, pend := range unacked {
		d := pend.start.Add(timeout)
		if !found || d.Before(earliest) {
			earliest, found = d, true
		}
	}
	return earliest, found
}

// rearmSweepTimer stops sweepTimer and reschedules it for the earliest
// still-outstanding deadline in unacked, or leaves it stopped when unacked is
// empty. Only ever called from runTargetWorker's own goroutine, so no
// synchronization is needed around the Stop/Reset sequence. Timer channels
// are synchronous (Go 1.23+, per go.mod), so Stop/Reset never leave a stale
// tick behind and no manual drain is needed.
func rearmSweepTimer(sweepTimer *time.Timer, unacked map[int]pendingProbe, timeout time.Duration) {
	sweepTimer.Stop()
	if d, ok := nextExpiry(unacked, timeout); ok {
		wait := max(time.Until(d), 0)
		sweepTimer.Reset(wait)
	}
}

// runTargetWorker drives one target's probe traffic with a single select loop
// that decouples sending from waiting on replies (see Apple ping.c's
// select()-based main loop, which never blocks sending on a reply):
//
//   - sendTimer fires immediately for the first probe (the first send is not
//     gated on a tick), then rearms itself for `interval` after every
//     subsequent fire. Later sends are never gated on a prior probe's reply,
//     so a target that never answers is still probed once per `interval`,
//     not once per `timeout`.
//   - ch delivers replies from the receiver goroutine; a reply is matched
//     against `unacked` by its (16-bit-masked) seq and, if found, recorded
//     as a success or ICMP error and removed. A reply with no matching
//     entry — a duplicate of an already-resolved seq, or one that arrived
//     after its entry was already swept as a timeout — is counted as a
//     duplicate or late reply via recentSeqHistory; any other non-matching
//     reply is silently discarded.
//   - sweepTimer fires at the earliest deadline among `unacked` and
//     records every entry that has aged past `timeout` as a loss.
//   - dnsTicker re-resolves the target every ResolveInterval.
//   - p.done ends the loop immediately, matching Stop()/Close().
//
// `unacked` is owned exclusively by this goroutine, so it needs no mutex.
//
// Count means "stop after sending Count probes", but since probes can be in
// flight concurrently, reaching Count does not imply the most recent probe
// (or any earlier one) has already been resolved: once the Count-th probe
// is sent, the loop stops sending but keeps servicing replies/sweeps —
// draining `unacked` — until every outstanding probe has been resolved,
// then returns. This is the same "wait out the stragglers before reporting
// the final tally" idea as ping.c's almost_done.
func (p *Pinger) runTargetWorker(t *stats.TargetStats, id int, interval, timeout time.Duration, ctx context.Context) {
	dstAddr := p.resolveTargetContext(ctx, t)

	seq := p.initialSeq
	// sendTimer fires once immediately (duration 0) for the first probe,
	// then is explicitly Reset(interval) after every fire that doesn't
	// terminate sending — see the case below. This reproduces the prior
	// implementation's "send right away, then pace off the ticker"
	// structure without a separate pre-loop send.
	sendTimer := time.NewTimer(0)
	defer sendTimer.Stop()

	resInterval := p.ResolveInterval
	if resInterval <= 0 {
		resInterval = 60 * time.Second
	}
	dnsTicker := time.NewTicker(resInterval)
	defer dnsTicker.Stop()

	// sweepTimer is created stopped: it's only armed once the first probe
	// is sent (rearmSweepTimer no-ops while unacked is empty).
	sweepTimer := time.NewTimer(timeout)
	sweepTimer.Stop()
	defer sweepTimer.Stop()

	p.mapMu.RLock()
	ch := p.targetChans[id]
	p.mapMu.RUnlock()

	payload := buildPayload(p.Size)

	unacked := make(map[int]pendingProbe)
	defer func() {
		for _, probe := range unacked {
			probe.stats.OnCancelled()
		}
	}()
	// hist remembers how recently-resolved wire seqs were resolved, so a
	// reply that no longer matches `unacked` can be classified as a
	// duplicate or a late arrival instead of just discarded (see
	// recentSeqHistory's doc). Owned exclusively by this goroutine, exactly
	// like `unacked`.
	hist := newRecentSeqHistory()
	doneSending := false // set once seq reaches p.Count (Count<=0 means unlimited)

	// replyCh stays nil (permanently blocking that select case) until the
	// first probe is actually written to the wire. A real ICMP reply can
	// never arrive before its request was sent, so this changes nothing
	// observable in production — but it matters for the select loop
	// itself: without this gate, the loop would be a ready reader on ch
	// from goroutine start, before `unacked` has its first entry, so any
	// reply delivered in that window would find no match and be discarded
	// as if it were a stale duplicate. Gating the case off until a send has
	// actually happened preserves the intuitive invariant that a reply is
	// only ever evaluated against an `unacked` that could possibly contain
	// it.
	var replyCh <-chan Reply

	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			return

		case <-dnsTicker.C:
			if newAddr := p.resolveTargetContext(ctx, t); newAddr != nil {
				dstAddr = newAddr
			}

		case <-sendTimer.C:
			if doneSending {
				continue
			}
			if dstAddr == nil {
				if addr := p.resolveTargetContext(ctx, t); addr != nil {
					dstAddr = addr
				} else {
					sendTimer.Reset(interval) // retry resolution on the next tick
					continue
				}
			}

			seq++

			probe := t.NewProbe()
			start, ok := p.sendProbeWithStats(t, probe, id, seq, payload, dstAddr)
			if ok {
				unacked[seq&seqMask] = pendingProbe{start: start, stats: probe}
				rearmSweepTimer(sweepTimer, unacked, timeout)
				replyCh = ch
			}

			if p.Count > 0 && seq >= p.Count {
				doneSending = true
			} else {
				sendTimer.Reset(interval)
			}

		case reply := <-replyCh:
			if p.settleReply(t, reply, unacked, hist) {
				rearmSweepTimer(sweepTimer, unacked, timeout)
			}

		case <-sweepTimer.C:
			p.sweepTimeouts(t, unacked, hist, timeout)
			rearmSweepTimer(sweepTimer, unacked, timeout)
		}

		if doneSending && len(unacked) == 0 {
			return
		}
	}
}

// settleReply resolves a reply against the worker's outstanding probes. A
// match records success or ICMP error and returns true (the sweep deadline
// may have moved); a reply for a recently settled seq is classified as a
// duplicate or late arrival. Called only from the worker's goroutine, which
// owns unacked and hist.
func (p *Pinger) settleReply(t *stats.TargetStats, reply Reply, unacked map[int]pendingProbe, hist *recentSeqHistory) bool {
	if pend, found := unacked[reply.Seq]; found {
		delete(unacked, reply.Seq)
		if reply.Err != "" {
			pend.stats.OnFailure(reply.Err)
		} else {
			// Prefer the RTT the receiver goroutine computed from the
			// payload-embedded send timestamp (see handleEchoReply):
			// it skips this goroutine hop entirely, so it isn't
			// inflated by scheduling delay between the receiver and
			// this select loop. reply.RTT is left at its zero value
			// whenever that timestamp couldn't be trusted (payload
			// too small for -s, signature mismatch, or a result
			// isPlausibleRTT rejected), in which case fall back to
			// this goroutine's own start-time bookkeeping.
			rtt := reply.RTT
			if rtt <= 0 {
				rtt = time.Since(pend.start)
			}
			pend.stats.OnSuccess(rtt, reply.TTL)
		}
		// Remember how this wire seq was resolved so a further reply
		// for it (a genuine network-level duplicate) can be
		// classified as a DUP below instead of silently discarded.
		hist.record(reply.Seq, resolvedAcked, pend.stats)
		return true
	}
	if entry, foundHist := hist.lookup(reply.Seq); foundHist {
		// No `unacked` entry, but this wire seq was recently
		// resolved: classify instead of silently discarding.
		switch entry.kind {
		case resolvedAcked:
			// A second reply for an already-resolved seq: a
			// network-level duplicate (routing loop, L2 duplication,
			// NAT/load-balancer anomaly). Never counted as Recv --
			// see TargetStats.Duplicates' doc -- so the loss rate
			// isn't understated.
			entry.stats.OnDuplicate()
		case resolvedTimeout:
			// Arrived after its probe was already swept as a loss:
			// the target is slow but reachable, not truly dropping
			// this probe. The Loss already recorded stands; see
			// TargetStats.LateReplies' doc.
			entry.stats.OnLateReply()
		}
	}
	// A reply matching neither `unacked` nor recent history is for a
	// seq this worker never resolved (or resolved so long ago that
	// recentSeqHistoryCap already evicted it): discard it silently.
	return false
}

// sweepTimeouts records a loss for every outstanding probe older than
// timeout, remembering each so a reply arriving later counts as late.
func (p *Pinger) sweepTimeouts(t *stats.TargetStats, unacked map[int]pendingProbe, hist *recentSeqHistory, timeout time.Duration) {
	now := time.Now()
	for wireSeq, pend := range unacked {
		if now.Sub(pend.start) >= timeout {
			delete(unacked, wireSeq)
			errMsg := p.applyLastErrSource("Timeout")
			pend.stats.OnFailure(errMsg)
			// Remember this seq timed out so a reply that shows up
			// later is classified as a late arrival by settleReply,
			// instead of silently discarded.
			hist.record(wireSeq, resolvedTimeout, pend.stats)
		}
	}
}

func buildPayload(size int) []byte {
	if size < 0 {
		size = 0
	}
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = 'A' // Fill with pattern
	}
	// Embed signature at the beginning if size permits
	if len(payload) >= len(payloadSignature) {
		copy(payload, payloadSignature)
	}
	return payload
}

// marshalProbe serializes an ICMP echo request for transmission.
//
// psh (the pseudo header argument to icmp.Message.Marshal) must stay nil
// here. For ICMPv4 it's ignored entirely, but for ICMPv6, x/net treats a
// non-nil psh as real pseudo-header bytes: it writes a 4-byte message
// length field at a fixed offset (2*net.IPv6len = 32) into the buffer
// and computes a checksum over it. A non-nil-but-empty slice (such as a
// pooled scratch buffer) satisfies "non-nil" without being an actual
// pseudo header, so that length write
// lands inside the ICMP payload once it's long enough to reach offset
// 32, silently corrupting it. Passing nil defers checksum computation to
// the kernel, which is required anyway since a raw ICMPv6 socket always
// recomputes and overwrites the checksum on send.
func marshalProbe(msg *icmp.Message) ([]byte, error) {
	return msg.Marshal(nil)
}
