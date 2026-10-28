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

// runWorker is a test helper that runs runTargetWorker (the production worker
// loop; see its doc) with a background context.
func (p *Pinger) runWorker(t *stats.TargetStats, id int, interval, timeout time.Duration) {
	p.runTargetWorker(t, id, interval, timeout, context.Background())
}

func (pc *PortChecker) check(t *stats.TargetStats, spec PortSpec, result *stats.PortCheckResult) {
	pc.checkContext(pc.ctx, t, spec, result)
}

func (p *Pinger) OpenHopSocket(dest string) (*HopSocket, error) {
	return p.OpenHopSocketContext(context.Background(), dest)
}
