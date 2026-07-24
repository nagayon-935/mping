package pinger

import (
	"context"
	"fmt"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// TraceRoute sends TTL-limited ICMP probes to dest and returns the IP address
// of each hop up to maxHops. Unreachable hops are represented as "*".
//
// dest is resolved once (not per hop) so every hop probes the same address
// even if dest is a hostname with multiple records. Each hop is sent via
// probeHopAddr, the same primitive the MTR engine uses, which registers a
// broadcast channel with the pinger's shared receiver when it is running (to
// avoid the macOS raw-socket race where the pinger's continuous ReadFrom
// consumes Time Exceeded replies before our socket can see them) and falls
// back to reading its own socket otherwise.
func (p *Pinger) TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error) {
	if dest == "" {
		return nil, fmt.Errorf("destination is empty")
	}
	if maxHops <= 0 {
		return nil, fmt.Errorf("maxHops must be > 0")
	}
	dstAddr, err := p.resolveIPAddr("ip", dest)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", dest, err)
	}

	sock, err := p.OpenHopSocket(dest)
	if err != nil {
		return nil, fmt.Errorf("open traceroute send socket: %w", err)
	}
	defer sock.Close()

	doneChan := make(chan struct{})
	defer close(doneChan)
	go func() {
		select {
		case <-ctx.Done():
			sock.Close()
		case <-doneChan:
		}
	}()

	traceID := p.NextTraceID()
	hops := make([]string, 0, maxHops)

	for ttl := 1; ttl <= maxHops; ttl++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		reply, err := p.probeHopAddr(ctx, sock, dstAddr, ttl, traceID, timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Marshal/send failure for this hop only; keep probing the rest.
			hops = append(hops, "*")
			continue
		}

		if !reply.Responded {
			hops = append(hops, "*")
			continue
		}

		srcIP := reply.SrcIP
		if srcIP == "" {
			srcIP = "*"
		} else if srcIP != "*" && p.AsnEnabled {
			info := p.getASNInfo(srcIP)
			if annotation := stats.FormatASN(info.Number, info.Org); annotation != "" {
				srcIP = fmt.Sprintf("%s(%s)", srcIP, annotation)
			}
		}
		hops = append(hops, srcIP)

		if reply.ReachedDest {
			break
		}
	}
	return hops, nil
}
