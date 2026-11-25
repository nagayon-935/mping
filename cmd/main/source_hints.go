package main

import (
	"net/netip"
	"sync"
	"time"
)

// sourceHintTTL bounds how long a destination's source stays cached. Route
// probes are cheap, and re-probing picks up a rotated temporary IPv6 address
// or a route change without waiting for a reload.
const sourceHintTTL = 30 * time.Second

// maxSourceHints caps the cache; when full it starts over, which only costs
// one probe per destination still being shown.
const maxSourceHints = 4096

type sourceHint struct {
	src string
	at  time.Time
}

// sourceHints reports, per destination, the source address the kernel's route
// selection uses. The per-family display hints (runEnv.dispV4/dispV6) come
// from the first target with a route, so they are wrong for a destination of
// the same family that leaves by another source, such as a global target
// next to a link-local one.
//
// forDst is called from the TUI draw goroutine and the supervisor's command
// loop, so the cache is guarded by mu.
type sourceHints struct {
	// probe is the UDP route probe for one destination; nil when the source
	// is fixed (-S, or -I limited to one family), where the display hints
	// are already exact.
	probe func(remote, network string) string
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]sourceHint
}

func newSourceHints(cfg config, env runEnv) *sourceHints {
	h := &sourceHints{now: time.Now}
	switch {
	case cfg.sourceAddr != "" || env.bindIP != "":
	case cfg.ifaceName != "":
		h.probe = interfaceRouteProbe(cfg.ifaceName)
	default:
		h.probe = getPreferredOutboundIPFn
	}
	return h
}

// forDst returns the source the route to dstIP uses, or "" when it is not
// known (no probe, no address yet, or no route), leaving the per-family hint
// to the caller. A nil *sourceHints answers "".
func (h *sourceHints) forDst(dstIP string) string {
	if h == nil || h.probe == nil {
		return ""
	}
	addr, err := netip.ParseAddr(dstIP)
	if err != nil {
		return ""
	}
	network := "udp6"
	if addr.Unmap().Is4() {
		network = "udp4"
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if hint, ok := h.cache[dstIP]; ok && now.Sub(hint.at) < sourceHintTTL {
		return hint.src
	}
	src := h.probe(dstIP, network)
	if h.cache == nil || len(h.cache) >= maxSourceHints {
		h.cache = make(map[string]sourceHint)
	}
	h.cache[dstIP] = sourceHint{src: src, at: now}
	return src
}
