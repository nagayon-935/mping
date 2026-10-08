package pinger

import (
	"context"
	"net"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// resolveIPAddrBounded applies resolveTimeout and aborts when Stop closes
// p.done, so a hung resolver cannot stall workers or Pinger.Wait.
func (p *Pinger) resolveIPAddrBounded(network, address string) (*net.IPAddr, error) {
	return p.resolveIPAddrContext(context.Background(), network, address)
}

// resolveTarget attempts DNS resolution and updates the target's IP.
// Returns the resolved address, or nil if resolution failed or the pinger
// was stopped mid-resolution.
func (p *Pinger) resolveTarget(t *stats.TargetStats) *net.IPAddr {
	return p.resolveTargetContext(context.Background(), t)
}

func (p *Pinger) getASN(ipStr string) string {
	return p.getASNInfo(ipStr).Number
}

// sendProbe marshals and sends an ICMP echo request. Returns the send time, or an error string.
func (p *Pinger) sendProbe(t *stats.TargetStats, id, seq int, payload []byte, dstAddr *net.IPAddr) (time.Time, bool) {
	return p.sendProbeWithStats(t, t.NewProbe(), id, seq, payload, dstAddr)
}

// runWorker drives one target's probe traffic with a single select loop
// that decouples sending from waiting on replies (see Apple ping.c's
// select()-based main loop, which never blocks sending on a reply):
//
//   - sendTimer fires immediately for the first probe (matching the prior
//     stop-and-wait runWorker, which never gated its first send on a
//     tick), then rearms itself for `interval` after every subsequent
//     fire. Unlike a stop-and-wait design, later sends are never gated on
//     a prior probe's reply, so a target that never answers is still
//     probed once per `interval`, not once per `timeout`.
//   - ch delivers replies from the receiver goroutine; a reply is matched
//     against `unacked` by its (16-bit-masked) seq and, if found, recorded
//     as a success or ICMP error and removed. A reply with no matching
//     entry — a duplicate of an already-resolved seq, or one that arrived
//     after its entry was already swept as a timeout — is silently
//     discarded, exactly as the prior stop-and-wait waitForReply did for
//     any non-matching reply.
//   - sweepTimer fires at the earliest deadline among `unacked` and
//     records every entry that has aged past `timeout` as a loss.
//   - dnsTicker re-resolves the target on the same cadence as before.
//   - p.done ends the loop immediately, matching Stop()/Close().
//
// `unacked` is owned exclusively by this goroutine, so it needs no mutex.
//
// Count still means "stop after sending Count probes", but since probes can
// now be in flight concurrently, reaching Count no longer implies the most
// recent probe (or any earlier one) has already been resolved: once the
// Count-th probe is sent, the loop stops sending but keeps servicing
// replies/sweeps — draining `unacked` — until every outstanding probe has
// been resolved, then returns. This is the same "wait out the stragglers
// before reporting the final tally" idea as ping.c's almost_done.
func (p *Pinger) runWorker(t *stats.TargetStats, id int, interval, timeout time.Duration) {
	p.runTargetWorker(t, id, interval, timeout, context.Background())
}

func (pc *PortChecker) check(t *stats.TargetStats, spec PortSpec, result *stats.PortCheckResult) {
	pc.checkContext(pc.ctx, t, spec, result)
}
