package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// makePingerFactory returns a closure that creates a configured pinger instance
// with the given payload size. The returned factory is called each time a new
// pinger is needed (initial start and after restart).
func makePingerFactory(targets []*stats.TargetStats, opts pinger.Options, cfg config, bind pinger.BindConfig) func(size int) pingerController {
	return func(size int) pingerController {
		p := newPinger(targets, opts)
		p.SetSource(bind.Source)
		p.SetInterfaceSources(bind.SourceV4, bind.SourceV6)
		p.SetInterface(cfg.ifaceName)
		p.SetSize(size)
		p.SetCount(cfg.count)
		p.SetResolveInterval(dnsResolveInterval)
		return p
	}
}

const pmtuWorkers = 8

type deferredPMTU func(context.Context, func(string))

// runPMTUWorkers bounds both resolver and probe concurrency and joins all work.
func runPMTUWorkers(ctx context.Context, count int, work func(int)) {
	var next int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range min(count, pmtuWorkers) {
		wg.Go(func() {
			for {
				mu.Lock()
				if next >= count || ctx.Err() != nil {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()
				work(i)
			}
		})
	}
	wg.Wait()
}

func pmtuLog(line string) string {
	return fmt.Sprintf("[%s] %s", clock(), tview.Escape(line))
}

// setupPMTU resolves targets and discovers IPv4 paths before choosing Size.
// IPv6 paths are returned as background work to start after regular ping.
func setupPMTU(makePinger func(size int) pingerController, cfg config, ifaceMTU int, targets []*stats.TargetStats, errOut io.Writer) (packetSize int, preLogs []string, deferred deferredPMTU) {
	packetSize = cfg.packetSize
	if !cfg.mtuEnabled {
		return
	}
	probe := makePinger(cfg.packetSize)
	defer probe.Close()
	defer probe.Stop()
	startPayload := pmtuMaxPayload
	if ifaceMTU > pmtuHeaderBytes {
		startPayload = ifaceMTU - pmtuHeaderBytes
	}
	type outcome struct {
		addr *net.IPAddr
		info pinger.PathMTUResult
		err  error
		logs []string
	}
	results := make([]outcome, len(targets))
	runPMTUWorkers(context.Background(), len(targets), func(i int) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		r := &results[i]
		r.addr, r.err = probe.ResolveTarget(ctx, targets[i].ResolveAddress())
		if r.err != nil {
			return
		}
		if r.addr == nil || r.addr.IP == nil {
			r.err = fmt.Errorf("resolver returned no IP address")
			return
		}
		if r.addr.IP.To4() == nil {
			return
		}
		r.info, r.err = probe.DiscoverPathMTU(ctx, r.addr.String(), startPayload, cfg.packetSize, func(line string) {
			r.logs = append(r.logs, pmtuLog(line))
		})
	})
	allIPv4, minPayload := true, 0
	var ipv6Targets []int
	for i, t := range targets {
		r := results[i]
		preLogs = append(preLogs, r.logs...)
		if r.addr != nil && r.addr.IP.To4() == nil && r.err == nil {
			allIPv4 = false
			ipv6Targets = append(ipv6Targets, i)
			continue
		}
		if r.err != nil {
			fmt.Fprintf(errOut, "PMTU discovery failed for %s: %v\n", t.Host(), r.err)
			continue
		}
		t.SetPathMTU(r.info)
		if minPayload == 0 || r.info.Payload < minPayload {
			minPayload = r.info.Payload
		}
	}
	if allIPv4 && minPayload > 0 {
		packetSize = minPayload
	}
	if len(ipv6Targets) == 0 {
		return
	}
	deferred = func(ctx context.Context, logf func(string)) {
		if ctx.Err() != nil {
			return
		}
		backgroundProbe := makePinger(cfg.packetSize)
		defer backgroundProbe.Close()
		defer backgroundProbe.Stop()
		var logMu sync.Mutex
		log := func(line string) {
			logMu.Lock()
			defer logMu.Unlock()
			if logf != nil {
				logf(pmtuLog(line))
			}
		}
		runPMTUWorkers(ctx, len(ipv6Targets), func(j int) {
			i := ipv6Targets[j]
			probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			info, err := backgroundProbe.DiscoverPathMTU(probeCtx, results[i].addr.String(), startPayload, cfg.packetSize, log)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log(fmt.Sprintf("PMTU discovery failed for %s: %v", targets[i].Host(), err))
				if info.Source != "os" || info.MTU < 1280 {
					return
				}
				info.Status = "estimated"
			}
			targets[i].SetPathMTU(info)
		})
	}
	return
}

// setupPortChecker parses port specs and starts a PortChecker if any specs are
// provided. Returns nil if no specs are given. bind is the -S/-I pair the ICMP
// pinger is already bound to (see makePingerFactory), so port checks probe over
// the same egress path.
func setupPortChecker(targets []*stats.TargetStats, portSpecs []pinger.PortSpec, interval, timeout time.Duration, bind pinger.BindConfig) *pinger.PortChecker {
	if len(portSpecs) == 0 {
		return nil
	}
	pc := pinger.NewPortChecker(targets, portSpecs, interval, timeout, bind)
	pc.Start()
	return pc
}
