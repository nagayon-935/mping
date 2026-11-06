package main

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// makePingerFactory returns a closure that creates a configured pinger instance
// with the given payload size. The returned factory is called each time a new
// pinger is needed (initial start and after restart).
func makePingerFactory(targets []*stats.TargetStats, opts pinger.Options, cfg config, bindIP string) func(size int) pingerController {
	return func(size int) pingerController {
		p := newPinger(targets, opts)
		p.SetSource(bindIP)
		p.SetInterface(cfg.ifaceName)
		p.SetSize(size)
		p.SetCount(cfg.count)
		p.SetResolveInterval(dnsResolveInterval)
		return p
	}
}

// setupPMTU runs PMTU discovery using a probe pinger and updates per-target
// sizes. It returns the discovered payload size and any pre-log messages.
func setupPMTU(makePinger func(size int) pingerController, cfg config, ifaceMTU int, targets []*stats.TargetStats, errOut io.Writer) (packetSize int, preLogs []string) {
	packetSize = cfg.packetSize
	if !cfg.mtuEnabled {
		return
	}
	probe := makePinger(cfg.packetSize)
	// probe.Start() is never called — discovery opens/closes
	// its own sockets internally. Stop() only closes the done channel
	// (safe to call without Start) and prevents any future goroutine
	// from blocking on it.
	defer probe.Stop()
	defer probe.Close()
	startPayload := pmtuMaxPayload
	if ifaceMTU > pmtuHeaderBytes {
		startPayload = ifaceMTU - pmtuHeaderBytes
	}
	// Progress lines are plain text ("[PMTU] payload=…"); escape them so the
	// Log pane does not read "[PMTU]" as a colour tag.
	type outcome struct {
		info pinger.PathMTUResult
		err  error
		logs []string
	}
	results := make([]outcome, len(targets))
	// The IPv4 search shares an echo ID/sequence, so discoveries are serial.
	// Family dispatch is exclusively based on each resolved destination IP.
	for i := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		results[i].info, results[i].err = probe.DiscoverPathMTU(ctx, targets[i].ResolveAddress(), startPayload, cfg.packetSize, func(line string) {
			results[i].logs = append(results[i].logs, fmt.Sprintf("[%s] %s", clock(), tview.Escape(line)))
		})
		cancel()
	}
	// Every destination has its own path. IPv6 PMTU describes that path and
	// does not change the regular ping payload. Keep the legacy IPv4 payload
	// adjustment only when every target was successfully searched as IPv4.
	allIPv4 := true
	minPayload := 0
	for i, t := range targets {
		preLogs = append(preLogs, results[i].logs...)
		if results[i].err != nil {
			fmt.Fprintf(errOut, "PMTU discovery failed for %s: %v\n", t.Host(), results[i].err)
			// A failed confirmation does not invalidate an already retrieved
			// kernel estimate. Keep its explicitly unconfirmed status.
			if info := results[i].info; info.Source == "os" && info.MTU >= 1280 {
				info.Status = "estimated"
				t.SetPathMTU(info)
			}
			allIPv4 = false
			continue
		}
		result := results[i].info
		t.SetPathMTU(result)
		ip, _ := netip.ParseAddr(result.IP)
		if !ip.Unmap().Is4() {
			allIPv4 = false
		}
		if minPayload == 0 || result.Payload < minPayload {
			minPayload = result.Payload
		}
	}
	if allIPv4 && minPayload > 0 {
		packetSize = minPayload
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
