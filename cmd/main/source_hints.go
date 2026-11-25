package main

import (
	"net/netip"
	"sync"
	"time"
)

const (
	// sourceHintTTL bounds how long a destination's source is trusted.
	// Re-probing picks up a rotated temporary IPv6 address or a route change
	// without waiting for a reload.
	sourceHintTTL = 30 * time.Second
	// sourceMissTTL is the shorter wait before re-probing a destination that
	// had no route, such as one behind an interface that is still coming up.
	sourceMissTTL = 3 * time.Second
	// maxSourceHints caps the cache; when full it starts over, which only
	// costs one probe per destination still in use.
	maxSourceHints = 4096
	// maxSourceProbes bounds concurrent background probes, so a large target
	// list expiring at once does not spawn a burst of route lookups.
	maxSourceProbes = 4
)

type sourceHint struct {
	src     string
	expires time.Time
}

// sourceHints reports, per destination, the source address the kernel's route
// selection uses. The per-family display hints (runEnv.dispV4/dispV6) come
// from the first target with a route, so they are wrong for a destination of
// the same family that leaves by another source, such as a global target
// next to a link-local one.
//
// lookup serves the TUI draw goroutine and never waits for a probe; resolve
// serves report capture and probes synchronously when needed. Both may run
// concurrently, so the cache is guarded by mu.
type sourceHints struct {
	// probe is the UDP route probe for one destination; nil when the source
	// is fixed (-S, or -I limited to one family), where the display hints
	// are already exact.
	probe func(remote, network string) string
	now   func() time.Time

	mu       sync.Mutex
	cache    map[string]sourceHint
	inflight map[string]bool
	slots    chan struct{}
	pending  sync.WaitGroup
}

func newSourceHints(cfg config, env runEnv) *sourceHints {
	h := &sourceHints{
		now:      time.Now,
		cache:    make(map[string]sourceHint),
		inflight: make(map[string]bool),
		slots:    make(chan struct{}, maxSourceProbes),
	}
	switch {
	case cfg.sourceAddr != "" || env.bindIP != "":
	case cfg.ifaceName != "":
		h.probe = interfaceRouteProbe(cfg.ifaceName)
	default:
		h.probe = getPreferredOutboundIPFn
	}
	return h
}

// lookup returns the cached source for dstIP without blocking, scheduling a
// background probe when the entry is missing or expired. Until a probe
// lands it answers the previous source, or "" when there is none, leaving
// the per-family hint to the caller. A nil *sourceHints answers "".
func (h *sourceHints) lookup(dstIP string) string {
	network, ok := h.networkFor(dstIP)
	if !ok {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	hint, found := h.cache[dstIP]
	if !found || !h.now().Before(hint.expires) {
		h.refreshLocked(dstIP, network)
	}
	return hint.src
}

// resolve returns the source the route to dstIP uses, probing synchronously
// unless a fresh answer is cached; "" means it is not known (no probe, no
// address yet, or no route). A nil *sourceHints answers "".
func (h *sourceHints) resolve(dstIP string) string {
	network, ok := h.networkFor(dstIP)
	if !ok {
		return ""
	}
	h.mu.Lock()
	if hint, found := h.cache[dstIP]; found && h.now().Before(hint.expires) {
		h.mu.Unlock()
		return hint.src
	}
	h.mu.Unlock()

	src := h.probe(dstIP, network)
	h.mu.Lock()
	h.storeLocked(dstIP, src)
	h.mu.Unlock()
	return src
}

// wait blocks until every background probe started so far has finished.
func (h *sourceHints) wait() {
	if h != nil {
		h.pending.Wait()
	}
}

// networkFor reports the UDP network to probe dstIP with; ok=false when
// there is nothing to probe (no probe configured, or not an IP literal).
func (h *sourceHints) networkFor(dstIP string) (network string, ok bool) {
	if h == nil || h.probe == nil {
		return "", false
	}
	addr, err := netip.ParseAddr(dstIP)
	if err != nil {
		return "", false
	}
	if addr.Unmap().Is4() {
		return "udp4", true
	}
	return "udp6", true
}

func (h *sourceHints) refreshLocked(dstIP, network string) {
	if h.inflight[dstIP] {
		return
	}
	h.inflight[dstIP] = true
	h.pending.Add(1)
	go func() {
		defer h.pending.Done()
		h.slots <- struct{}{}
		src := h.probe(dstIP, network)
		<-h.slots

		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.inflight, dstIP)
		h.storeLocked(dstIP, src)
	}()
}

func (h *sourceHints) storeLocked(dstIP, src string) {
	ttl := sourceHintTTL
	if src == "" {
		ttl = sourceMissTTL
	}
	if _, found := h.cache[dstIP]; !found && len(h.cache) >= maxSourceHints {
		h.cache = make(map[string]sourceHint)
	}
	h.cache[dstIP] = sourceHint{src: src, expires: h.now().Add(ttl)}
}
