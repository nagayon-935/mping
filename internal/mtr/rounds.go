package mtr

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// fanOut sends one TTL-limited probe per TTL in [firstTTL, lastTTL] concurrently. Each
// worker writes its own result slot before the caller inspects the round.
// When record is set, it can publish each completed sample immediately and
// must be safe to call concurrently. sem bounds probes across the Engine.
func fanOut(ctx context.Context, prober HopProber, sock HopSocket, dest string, timeout time.Duration, sem chan struct{}, firstTTL, lastTTL int, record func(ttl int, reply pinger.HopReply)) []pinger.HopReply {
	replies := make([]pinger.HopReply, lastTTL-firstTTL+1)
	var wg sync.WaitGroup
	for i := range replies {
		wg.Go(func() {
			if !acquire(ctx, sem) {
				return
			}
			defer release(sem)

			ttl := firstTTL + i
			traceID := prober.NextTraceID()
			reply, err := prober.ProbeHop(ctx, sock, dest, ttl, traceID, timeout)
			if err != nil || ctx.Err() != nil {
				return
			}
			replies[i] = reply
			if record != nil {
				record(ttl, reply)
			}
		})
	}
	wg.Wait()
	return replies
}

// routeLength uses the first echo reply to determine the destination TTL. If
// the destination is silent, the first matched unreachable reply explains
// the failure. A round with no terminal reply represents a partial path.
func routeLength(dest string, replies []pinger.HopReply) (int, error) {
	var unreachable pinger.HopReply
	for i, reply := range replies {
		if reply.ReachedDest {
			return i + 1, nil
		}
		if unreachable.UnreachableReason == "" && reply.UnreachableReason != "" {
			unreachable = reply
		}
	}
	if err := unreachableError(dest, unreachable); err != nil {
		return 0, err
	}
	return len(replies), nil
}

func unreachableError(dest string, reply pinger.HopReply) error {
	if reply.UnreachableReason == "" {
		return nil
	}
	if reply.SrcIP != "" {
		return fmt.Errorf("%s: %s (reported by %s)", dest, reply.UnreachableReason, reply.SrcIP)
	}
	return fmt.Errorf("%s: %s", dest, reply.UnreachableReason)
}

// routeRound requires a destination Echo before probing individual hops above
// the silent-destination limit. The high-TTL check samples only reachability.
func routeRound(ctx context.Context, prober HopProber, sock HopSocket, dest string, cfg Config, sem chan struct{}, lastTTL int, record func(ttl int, reply pinger.HopReply)) ([]pinger.HopReply, int, error) {
	firstLimit := lastTTL
	if cfg.limitUnresponsive {
		firstLimit = min(lastTTL, unresponsiveMaxHops)
	}
	var extension []pinger.HopReply
	var reachability pinger.HopReply
	probeExtension := func() {
		if ctx.Err() != nil {
			return
		}
		const reachabilityTTL = 255
		reachability = fanOut(ctx, prober, sock, dest, cfg.HopTimeout, sem, reachabilityTTL, reachabilityTTL, nil)[0]
		if reachability.ReachedDest && ctx.Err() == nil {
			extension = fanOut(ctx, prober, sock, dest, cfg.HopTimeout, sem, firstLimit+1, lastTTL, record)
		}
	}
	var wg sync.WaitGroup
	if firstLimit < lastTTL && record != nil {
		// Once a long route is known, check reachability alongside its first
		// hops so one slow intermediate hop cannot delay the extension.
		wg.Go(probeExtension)
	}
	replies := fanOut(ctx, prober, sock, dest, cfg.HopTimeout, sem, 1, firstLimit, record)
	if firstLimit < lastTTL && record == nil && !slices.ContainsFunc(replies, func(reply pinger.HopReply) bool {
		return reply.ReachedDest
	}) {
		probeExtension()
	}
	wg.Wait()
	replies = append(replies, extension...)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	hopCount, err := routeLength(dest, replies)
	if err != nil && reachability.ReachedDest {
		// The destination answered even though no individual hop probe
		// identified its TTL. Publish the partial route.
		return replies, len(replies), nil
	}
	if err == nil && reachability.UnreachableReason != "" && !replies[hopCount-1].ReachedDest {
		err = unreachableError(dest, reachability)
	}
	return replies, hopCount, err
}

// discover publishes the path up to the first destination reply or MaxHops.
// On failure or cancellation it preserves the last discovered route.
func discover(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, cfg Config, sem chan struct{}, ann *hopAnnotator) (int, error) {
	replies, hopCount, err := routeRound(ctx, prober, sock, dest, cfg, sem, cfg.MaxHops, nil)
	if err != nil {
		return 0, err
	}

	// Resize the path, including TTLs that failed to send or got no reply.
	mtr.TruncateLen(hopCount)
	mtr.EnsureLen(hopCount)
	for i, reply := range replies[:hopCount] {
		if reply.Responded && reply.SrcIP != "" && reply.UnreachableReason == "" {
			mtr.SetIPAnnotated(i+1, reply.SrcIP, ann.cached(reply.SrcIP))
			ann.note(reply.SrcIP)
		}
	}
	return hopCount, nil
}

// probe records one sample per hop, including TTLs that missed discovery.
// Timeouts and explicit unreachable replies are loss. Unsent probes do not
// count toward statistics.
func probe(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, hopCount int, cfg Config, sem chan struct{}, ann *hopAnnotator) error {
	_, _, err := routeRound(ctx, prober, sock, dest, cfg, sem, hopCount, func(ttl int, reply pinger.HopReply) {
		if reply.Responded && reply.UnreachableReason == "" {
			mtr.RecordReplyAnnotated(ttl, reply.SrcIP, ann.cached(reply.SrcIP), reply.RTT)
			ann.note(reply.SrcIP)
		} else {
			mtr.RecordLoss(ttl)
		}
	})
	return err
}

// acquire blocks until it obtains a slot in sem or ctx is cancelled,
// whichever comes first, returning false in the latter case (the caller
// should then return without calling release). A nil sem disables the
// bound entirely (acquire always succeeds immediately) — used only by
// tests that construct discover/probe calls without going through
// NewEngine/Start.
func acquire(ctx context.Context, sem chan struct{}) bool {
	if sem == nil {
		return true
	}
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// release returns the slot acquire took. No-op when sem is nil, mirroring
// acquire's bypass.
func release(sem chan struct{}) {
	if sem == nil {
		return
	}
	<-sem
}
