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

// dscpFor returns the override belonging to this target instance. Without an
// override, the write uses the socket-wide default armed by Start.
func (p *Pinger) dscpFor(t *stats.TargetStats) (int, bool) {
	p.mapMu.RLock()
	defer p.mapMu.RUnlock()
	if p.TargetDSCP == nil {
		return 0, false
	}
	v, ok := p.TargetDSCP[t]
	return v, ok
}

// getWriteFunc returns the appropriate ICMP message type and write function
// for the given destination address. dscp, when ok, is attached as an
// explicit per-packet ipv4.ControlMessage.TOS / ipv6.ControlMessage.
// TrafficClass on every WriteTo the returned func makes — overriding, for
// this target only, the socket-wide default Start() set via SetTOS/
// SetTrafficClass (or the OS default when that wasn't set either).
func (p *Pinger) getWriteFunc(dstAddr *net.IPAddr, dscp int, ok bool) (icmp.Type, func([]byte, net.Addr) (int, error), string) {
	isV4 := dstAddr.IP.To4() != nil
	if isV4 {
		if p.connV4 != nil {
			// dscp/ok are intentionally unused here: x/net's
			// ipv4.ControlMessage has no TOS field, so IPv4 has no
			// per-packet write-side hook to attach a per-target override
			// to (see PacketConnV4.SetTOS's doc) — every IPv4 target gets
			// only the socket-wide default Start() armed via SetTOS.
			return ipv4.ICMPTypeEcho, func(b []byte, dst net.Addr) (int, error) {
				return p.connV4.WriteTo(b, nil, dst)
			}, ""
		}
		return nil, nil, "No IPv4 Conn"
	}
	if p.connV6 != nil {
		var cm *ipv6.ControlMessage
		if ok {
			cm = &ipv6.ControlMessage{TrafficClass: dscp}
		}
		return ipv6.ICMPTypeEchoRequest, func(b []byte, dst net.Addr) (int, error) {
			return p.connV6.WriteTo(b, cm, dst)
		}, ""
	}
	return nil, nil, "No IPv6 Conn"
}

func (p *Pinger) sendProbeWithStats(t *stats.TargetStats, probe stats.Probe, id, seq int, payload []byte, dstAddr *net.IPAddr) (time.Time, bool) {
	dscp, dscpOK := p.dscpFor(t)
	msgType, writeFunc, errStr := p.getWriteFunc(dstAddr, dscp, dscpOK)
	if writeFunc == nil {
		probe.OnFailure(errStr)
		return time.Time{}, false
	}

	// start is captured before the payload is marshaled (rather than right
	// before the write, as before) because embedSendTimestamp needs it
	// embedded in `payload` first. This is the same instant used for both
	// the embedded timestamp and the pendingProbe fallback in runWorker, so
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
		p.log(t, seq, "SendError", 0, 0, err.Error())
		return time.Time{}, false
	}

	probe.IncSent()
	return start, true
}

// pendingProbe tracks one in-flight probe: the logical (unbounded) seq used
// for CSV logging, and the time it was sent (for RTT and expiry).
type pendingProbe struct {
	logicalSeq int
	start      time.Time
	stats      stats.Probe
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
// its logical seq (for CSV logging, matching pendingProbe.logicalSeq), and
// the original send time (for the same RTT fallback runWorker's normal
// success path uses when no payload-embedded timestamp is available).
type recentEntry struct {
	wireSeq    int
	logicalSeq int
	kind       resolutionKind
	start      time.Time
	stats      stats.Probe
}

// recentSeqHistory is a small, size-bounded FIFO cache mapping a
// just-resolved wire seq to how it was resolved. runWorker consults it only
// when a reply doesn't match anything in `unacked`, to tell a duplicate or a
// late arrival apart from a genuinely unrecognized/bogus seq (which stays
// silently discarded, exactly as before this feature existed).
//
// Capacity, not time, bounds it (see recentSeqHistoryCap's doc for why a
// bounded size rather than a time window is what keeps this safe across the
// 16-bit wire seq wraparound). It is owned exclusively by runWorker's own
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
func (h *recentSeqHistory) record(wireSeq, logicalSeq int, kind resolutionKind, start time.Time, probe stats.Probe) {
	if el, ok := h.index[wireSeq]; ok {
		h.order.Remove(el)
	}
	el := h.order.PushBack(recentEntry{wireSeq: wireSeq, logicalSeq: logicalSeq, kind: kind, start: start, stats: probe})
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
// whether unacked is non-empty. Because runWorker only ever inserts entries
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

// rearmSweepTimer stops/drains sweepTimer and reschedules it for the
// earliest still-outstanding deadline in unacked, or leaves it stopped when
// unacked is empty. Only ever called from runWorker's own goroutine, so no
// synchronization is needed around the Stop/drain/Reset sequence.
func rearmSweepTimer(sweepTimer *time.Timer, unacked map[int]pendingProbe, timeout time.Duration) {
	if !sweepTimer.Stop() {
		select {
		case <-sweepTimer.C:
		default:
		}
	}
	if d, ok := nextExpiry(unacked, timeout); ok {
		wait := time.Until(d)
		if wait < 0 {
			wait = 0
		}
		sweepTimer.Reset(wait)
	}
}

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
	if !sweepTimer.Stop() {
		<-sweepTimer.C
	}
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
					sendTimer.Reset(interval) // retry resolution on the next tick, as before
					continue
				}
			}

			seq++

			probe := t.NewProbe()
			start, ok := p.sendProbeWithStats(t, probe, id, seq, payload, dstAddr)
			if ok {
				unacked[seq&seqMask] = pendingProbe{logicalSeq: seq, start: start, stats: probe}
				rearmSweepTimer(sweepTimer, unacked, timeout)
				replyCh = ch
			}

			if p.Count > 0 && seq >= p.Count {
				doneSending = true
			} else {
				sendTimer.Reset(interval)
			}

		case reply := <-replyCh:
			if pend, found := unacked[reply.Seq]; found {
				delete(unacked, reply.Seq)
				if reply.Err != "" {
					pend.stats.OnFailure(reply.Err)
					p.log(t, pend.logicalSeq, "ICMPError", 0, 0, reply.Err)
				} else {
					// Prefer the RTT the receiver goroutine computed from the
					// payload-embedded send timestamp (see handleEchoReply):
					// it skips this goroutine hop entirely, so it isn't
					// inflated by scheduling delay between the receiver and
					// this select loop. reply.RTT is left at its zero value
					// whenever that timestamp couldn't be trusted (payload
					// too small for -s, signature mismatch, or a result
					// isPlausibleRTT rejected), in which case fall back to
					// this goroutine's own start-time bookkeeping, exactly
					// as before this feature existed.
					rtt := reply.RTT
					if rtt <= 0 {
						rtt = time.Since(pend.start)
					}
					pend.stats.OnSuccess(rtt, reply.TTL, reply.DSCP)
					p.log(t, pend.logicalSeq, "OK", rtt, reply.TTL, "")
				}
				// Remember how this wire seq was resolved so a further reply
				// for it (a genuine network-level duplicate) can be
				// classified as a DUP below instead of silently discarded.
				hist.record(reply.Seq, pend.logicalSeq, resolvedAcked, pend.start, pend.stats)
				rearmSweepTimer(sweepTimer, unacked, timeout)
			} else if entry, foundHist := hist.lookup(reply.Seq); foundHist {
				// No `unacked` entry, but this wire seq was recently
				// resolved: classify instead of silently discarding.
				rtt := reply.RTT
				if rtt <= 0 {
					rtt = time.Since(entry.start)
				}
				switch entry.kind {
				case resolvedAcked:
					// A second reply for an already-resolved seq: a
					// network-level duplicate (routing loop, L2 duplication,
					// NAT/load-balancer anomaly). Never counted as Recv --
					// see TargetStats.Duplicates' doc -- so the loss rate
					// isn't understated.
					entry.stats.OnDuplicate()
					p.log(t, entry.logicalSeq, "DUP", rtt, reply.TTL, "")
				case resolvedTimeout:
					// Arrived after its probe was already swept as a loss:
					// the target is slow but reachable, not truly dropping
					// this probe. The Loss already recorded stands; see
					// TargetStats.LateReplies' doc.
					entry.stats.OnLateReply()
					p.log(t, entry.logicalSeq, "LateReply", rtt, reply.TTL, "")
				}
			}
			// A reply matching neither `unacked` nor recent history is for a
			// seq this worker never resolved (or resolved so long ago that
			// recentSeqHistoryCap already evicted it): discard it silently,
			// matching the prior waitForReply's behavior for any
			// non-matching reply.

		case <-sweepTimer.C:
			now := time.Now()
			for wireSeq, pend := range unacked {
				if now.Sub(pend.start) >= timeout {
					delete(unacked, wireSeq)
					errMsg := p.applyLastErrSource("Timeout")
					pend.stats.OnFailure(errMsg)
					p.log(t, pend.logicalSeq, "Timeout", 0, 0, "Request timed out")
					// Remember this seq timed out so a reply that shows up
					// later can be classified as a late arrival below,
					// instead of silently discarded.
					hist.record(wireSeq, pend.logicalSeq, resolvedTimeout, pend.start, pend.stats)
				}
			}
			rearmSweepTimer(sweepTimer, unacked, timeout)
		}

		if doneSending && len(unacked) == 0 {
			return
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
// and computes a checksum over it. A non-nil-but-empty slice (as this
// package used to pass via a pooled buffer for size<=1400) satisfies
// "non-nil" without being an actual pseudo header, so that length write
// lands inside the ICMP payload once it's long enough to reach offset
// 32, silently corrupting it. Passing nil defers checksum computation to
// the kernel, which is required anyway since a raw ICMPv6 socket always
// recomputes and overwrites the checksum on send.
func marshalProbe(msg *icmp.Message) ([]byte, error) {
	return msg.Marshal(nil)
}
