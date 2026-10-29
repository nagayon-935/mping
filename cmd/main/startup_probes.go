package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// makePingerFactory returns a closure that creates a configured pinger instance
// with the given payload size. The returned factory is called each time a new
// pinger is needed (initial start and after restart).
func makePingerFactory(targets []*stats.TargetStats, opts pinger.Options, cfg config, bindIP string, logFile io.Writer) func(size int) pingerController {
	return func(size int) pingerController {
		p := newPinger(targets, opts)
		p.SetSource(bindIP)
		p.SetInterface(cfg.ifaceName)
		p.SetSize(size)
		p.SetCount(cfg.count)
		p.SetResolveInterval(dnsResolveInterval)
		if logFile != nil {
			p.SetLogWriter(logFile)
		}
		return p
	}
}

// setupPMTU runs PMTU discovery using a probe pinger and updates per-target
// sizes. It returns the discovered payload size and any pre-log messages.
func setupPMTU(makePinger func(size int) pingerController, cfg config, ifaceMTU int, targets []*stats.TargetStats, firstHost string, errOut io.Writer) (packetSize int, preLogs []string) {
	packetSize = cfg.packetSize
	if !cfg.mtuEnabled {
		return
	}
	if cfg.ipv6Only {
		fmt.Fprintln(errOut, "Warning: PMTU discovery disabled for IPv6")
		return
	}
	probe := makePinger(cfg.packetSize)
	// probe.Start() is never called — DiscoverMaxPayload opens/closes
	// its own sockets internally. Stop() only closes the done channel
	// (safe to call without Start) and prevents any future goroutine
	// from blocking on it.
	defer probe.Stop()
	startPayload := pmtuMaxPayload
	if ifaceMTU > pmtuHeaderBytes {
		startPayload = ifaceMTU - pmtuHeaderBytes
	}
	// Progress lines are plain text ("[PMTU] payload=…"); escape them so the
	// Log pane does not read "[PMTU]" as a colour tag.
	maxPayload, bottleneckIP, err := probe.DiscoverMaxPayload(context.Background(), firstHost, startPayload, cfg.packetSize, func(line string) {
		preLogs = append(preLogs, fmt.Sprintf("[%s] %s", clock(), tview.Escape(line)))
	})
	if err != nil {
		fmt.Fprintf(errOut, "PMTU discovery failed: %v\n", err)
		return
	}
	packetSize = maxPayload
	for _, t := range targets {
		t.SetPMTU(maxPayload)
		if bottleneckIP != "" {
			t.SetPMTUBottleneckIP(bottleneckIP)
		}
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
