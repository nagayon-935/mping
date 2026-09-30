package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// pingerController manages the lifecycle of a pinger instance.
// Lifecycle: Start() → [running] → Stop() → Wait() → (done)
// Stop() signals the pinger to stop; Wait() blocks until all goroutines exit.
// Close() closes underlying network connections immediately (use only when
// Start() was never called or after Wait() has returned).
type pingerController interface {
	Start(interval, timeout time.Duration) error
	Stop()
	Wait()
	WaitWorkers()
	Close()
	DiscoverMaxPayload(ctx context.Context, dest string, start int, min int, logf func(string)) (int, string, error)
	TraceRoute(ctx context.Context, dest string, maxHops int, timeout time.Duration) ([]string, error)
	SetSource(ip string)
	SetInterface(name string)
	SetSize(size int)
	SetCount(count int)
	SetResolveInterval(interval time.Duration)
	SetLogWriter(w io.Writer)
	// MTRProber returns an mtr.HopProber view onto this controller, used to
	// wire up the MTR engine without the caller needing to downcast to a
	// concrete pinger type.
	MTRProber() mtr.HopProber
}

type pingerAdapter struct {
	*pinger.Pinger
}

func (p *pingerAdapter) SetSource(ip string) {
	p.Source = ip
}

// SetInterface arms true interface binding (see internal/pinger/bindif_*.go)
// on top of the source-IP bind SetSource already performs. name is cfg.
// ifaceName, which is only non-empty when -I was passed, so this is a no-op
// exactly when it always was: -S-only runs never touch Interface.
func (p *pingerAdapter) SetInterface(name string) {
	p.Interface = name
}

func (p *pingerAdapter) SetSize(size int) {
	p.Size = size
}

func (p *pingerAdapter) SetCount(count int) {
	p.Count = count
}

func (p *pingerAdapter) SetResolveInterval(interval time.Duration) {
	p.ResolveInterval = interval
}

func (p *pingerAdapter) SetLogWriter(w io.Writer) {
	p.LogWriter = w
}

func (p *pingerAdapter) Close() {
	p.Pinger.Close()
}

func (p *pingerAdapter) MTRProber() mtr.HopProber {
	return &pingerMTRAdapter{p: p.Pinger}
}

// pingerMTRAdapter wraps *pinger.Pinger to satisfy mtr.HopProber.
type pingerMTRAdapter struct {
	p *pinger.Pinger
}

func (a *pingerMTRAdapter) OpenHopSocket(ctx context.Context, dest string) (mtr.HopSocket, error) {
	return a.p.OpenHopSocketContext(ctx, dest)
}

func (a *pingerMTRAdapter) ProbeHop(ctx context.Context, sock mtr.HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	hopSock, ok := sock.(*pinger.HopSocket)
	if !ok {
		return pinger.HopReply{}, fmt.Errorf("unexpected socket type in ProbeHop")
	}
	return a.p.ProbeHop(ctx, hopSock, dest, ttl, traceID, timeout)
}

func (a *pingerMTRAdapter) NextTraceID() int { return a.p.NextTraceID() }
func (a *pingerMTRAdapter) ASNInfoFor(ip string) pinger.ASNInfo {
	return a.p.GetASNInfoFor(ip)
}

var newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
	return &pingerAdapter{Pinger: pinger.NewPingerWithOptions(targets, opts)}
}
