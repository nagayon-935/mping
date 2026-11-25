package mtr

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// fanOut sends one TTL-limited probe per TTL in 1..n concurrently. Each
// worker writes its own result slot before the caller inspects the round.
// When record is set, it can publish each completed sample immediately and
// must be safe to call concurrently. sem bounds probes across the Engine.
func fanOut(ctx context.Context, prober HopProber, sock HopSocket, dest string, timeout time.Duration, sem chan struct{}, n int, record func(ttl int, reply pinger.HopReply)) []pinger.HopReply {
	replies := make([]pinger.HopReply, n)
	var wg sync.WaitGroup
	for i := range replies {
		wg.Go(func() {
			if !acquire(ctx, sem) {
				return
			}
			defer release(sem)

			traceID := prober.NextTraceID()
			reply, err := prober.ProbeHop(ctx, sock, dest, i+1, traceID, timeout)
			if err != nil || ctx.Err() != nil {
				return
			}
			replies[i] = reply
			if record != nil {
				record(i+1, reply)
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
	if unreachable.UnreachableReason == "" {
		return len(replies), nil
	}
	if unreachable.SrcIP != "" {
		return 0, fmt.Errorf("%s: %s (reported by %s)", dest, unreachable.UnreachableReason, unreachable.SrcIP)
	}
	return 0, fmt.Errorf("%s: %s", dest, unreachable.UnreachableReason)
}

// discover publishes the path up to the first destination reply or MaxHops.
// On failure or cancellation it preserves the last discovered route.
func discover(ctx context.Context, prober HopProber, sock HopSocket, mtr *stats.MTRStats, dest string, cfg Config, sem chan struct{}, ann *hopAnnotator) (int, error) {
	replies := fanOut(ctx, prober, sock, dest, cfg.HopTimeout, sem, cfg.MaxHops, nil)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	hopCount, err := routeLength(dest, replies)
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
	replies := fanOut(ctx, prober, sock, dest, cfg.HopTimeout, sem, hopCount, func(ttl int, reply pinger.HopReply) {
		if reply.Responded && reply.UnreachableReason == "" {
			mtr.RecordReplyAnnotated(ttl, reply.SrcIP, ann.cached(reply.SrcIP), reply.RTT)
			ann.note(reply.SrcIP)
		} else {
			mtr.RecordLoss(ttl)
		}
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := routeLength(dest, replies)
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
