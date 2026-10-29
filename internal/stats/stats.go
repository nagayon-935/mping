package stats

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	historySize   = 3000 // 30s of data at the minimum UI refresh rate (100ms per point)
	jitterDivisor = 16   // RFC 1889 §A.8 smoothing factor: J = J + (|D| - J) / 16
)

// targetIDs allocates TargetStats.ID() values.
var targetIDs atomic.Uint64

type IPChange struct {
	At time.Time `json:"at"`
	IP string    `json:"ip"`
}

// TargetStats holds the statistics for a single ping target.
//
// Every field is unexported and guarded by mu: writers go through the
// Set*/On*/Inc* methods, and readers take a consistent copy through GetView
// (or one of the narrower accessors below), so no caller can read or write a
// counter without holding the lock. id and host never change after
// NewTargetStats and are exposed through ID() and Host().
type TargetStats struct {
	id              uint64 // never reused within this process
	host            string // as configured; the display label is displayNameLocked
	startedAt       time.Time
	windowStartedAt time.Time
	dscp            string
	ipHistory       []IPChange
	ipChanges       int
	cancelled       int

	ip               string
	asn              string
	country          string
	org              string
	ptr              string // reverse DNS (PTR) hostname for IP, when --ptr is enabled
	dnsServer        string
	ifaceMTU         int
	pmtu             int
	pmtuBottleneckIP string
	traceHops        []string
	portResults      []*PortCheckResult
	mtrStats         *MTRStats
	sent             int
	recv             int
	loss             int
	// duplicates counts replies that repeat a seq already resolved (either
	// by a successful reply or an ICMP error) -- a network-level dup
	// (routing loop, L2 duplication, NAT/load-balancer anomaly), not a
	// second successful probe. Never folded into recv: doing so would
	// understate the real loss rate, exactly the reason Apple's ping.c
	// decrements nreceived when it detects a dup.
	duplicates int
	// lateReplies counts replies that arrive for a seq whose `unacked`
	// entry was already swept as a timeout (and so already counted in
	// loss). Tracked separately so a target that's simply slow can be told
	// apart from one that's truly dropping packets -- mirrors ping.c's
	// -W/nrcvtimeout. The loss already recorded for that probe is left
	// standing; a late reply never decrements it.
	lateReplies int
	lastRTT     time.Duration
	lastTTL     int
	// lastDSCP is the TOS (IPv4) / TrafficClass (IPv6) byte observed on the
	// most recent successful reply's own IP header — i.e. what DSCP marking
	// actually arrived, after any re-marking or bleaching along the return
	// path. Zero both as the pre-first-reply default and when the platform
	// didn't supply a control message; same ambiguity as lastTTL's zero
	// value, and for the same reason (see pinger.Reply.DSCP).
	lastDSCP     int
	lastLossTime time.Time
	lastError    string

	// RTT statistics and history (ring buffer for Sparkline).
	rtt rttAccumulator

	// Jitter (RFC 1889)
	jitter           int64 // Stored as nanoseconds for smooth calculation
	events           []Event
	eventsDropped    int
	probeEpoch       uint64 // Reset invalidates every probe from the previous window.
	displayName      string
	resolveAddress   string
	health           targetHealth
	historyTimes     []int64
	measurementState string
	eventSink        func(Event)

	mu sync.RWMutex
}

func NewTargetStats(host string) *TargetStats {
	now := time.Now()
	return &TargetStats{
		host:             host,
		id:               targetIDs.Add(1),
		startedAt:        now,
		windowStartedAt:  now,
		measurementState: "running",
	}
}

// ID returns the target's immutable identity.
func (t *TargetStats) ID() uint64 { return t.id }

// Host returns the host exactly as configured (hostname or literal IP). It
// never changes; the label shown to the user is DisplayName.
func (t *TargetStats) Host() string { return t.host }

// SetDSCP records the configured outbound DSCP marking (a codepoint name or
// number) shown in the target's details. It does not change what is sent.
func (t *TargetStats) SetDSCP(dscp string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dscp = dscp
}

// MTR returns the MTRStats for this target, creating it lazily on first call.
func (t *TargetStats) MTR() *MTRStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mtrStats == nil {
		t.mtrStats = NewMTRStats()
	}
	return t.mtrStats
}

func (t *TargetStats) SetIP(ip string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ip != ip {
		t.recordEventLocked("dns", "IP: "+t.ip+" → "+ip)
		if t.ip != "" {
			t.ipChanges++
		}
		t.ipHistory = append(t.ipHistory, IPChange{At: time.Now(), IP: ip})
		if len(t.ipHistory) > 64 {
			t.ipHistory = append([]IPChange(nil), t.ipHistory[len(t.ipHistory)-64:]...)
		}
	}
	t.ip = ip
}

func (t *TargetStats) SetASNInfo(number, country, org string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asn = number
	t.country = country
	t.org = org
}

// SetPTR records the resolved PTR (reverse DNS) hostname for t's IP.
func (t *TargetStats) SetPTR(name string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ptr = name
}

func (t *TargetStats) SetDNSServer(dnsServer string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dnsServer = dnsServer
}

func (t *TargetStats) SetTraceHops(hops []string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.traceHops = make([]string, len(hops))
	copy(t.traceHops, hops)
}

func (t *TargetStats) SetIfaceMTU(mtu int) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ifaceMTU = mtu
}

func (t *TargetStats) SetPMTU(pmtu int) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pmtu = pmtu
}

func (t *TargetStats) SetPMTUBottleneckIP(ip string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pmtuBottleneckIP = ip
}

// SetPortResults replaces the port check results slice atomically.
func (t *TargetStats) SetPortResults(results []*PortCheckResult) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.portResults = results
}

func (t *TargetStats) IncSent() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent++
}

func (t *TargetStats) OnSuccess(rtt time.Duration, ttl int) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onSuccessLocked(rtt, ttl)
}

func (t *TargetStats) onSuccessLocked(rtt time.Duration, ttl int) {
	if t.lastError != "" {
		t.recordEventLocked("recovery", "Ping replies resumed")
	}
	// RFC 1889 Jitter Calculation: J = J + (|D| - J) / 16
	if t.recv > 0 {
		t.jitter = updateJitter(t.jitter, rtt, t.lastRTT)
	}

	t.recv++
	t.lastRTT = rtt
	t.lastTTL = ttl
	t.rtt.record(rtt)
	now := time.Now()
	t.appendHistoryLocked(rtt, now)
	t.recordOutcomeLocked(now, true)

	t.lastError = ""
}

// OnDuplicate records a reply that repeats a seq already resolved. See the
// Duplicates field's doc for why this must never be folded into Recv.
func (t *TargetStats) OnDuplicate() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.duplicates++
}

// OnLateReply records a reply that arrived after its probe's `unacked`
// entry was already swept as a timeout. See the LateReplies field's doc for
// why the Loss already recorded for that probe is left untouched here.
func (t *TargetStats) OnLateReply() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lateReplies++
}

func (t *TargetStats) OnFailure(reason string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFailureLocked(reason)
}

func (t *TargetStats) onFailureLocked(reason string) {
	t.loss++
	t.lastLossTime = time.Now()
	t.lastError = reason
	t.recordEventLocked("ping", reason)
	t.appendHistoryLocked(0, t.lastLossTime)
	t.recordOutcomeLocked(t.lastLossTime, false)
}

func (t *TargetStats) Reset() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.probeEpoch++
	t.windowStartedAt = time.Now()
	t.recordEventLocked("reset", "Statistics reset")
	t.sent = 0
	t.cancelled = 0
	t.recv = 0
	t.loss = 0
	t.duplicates = 0
	t.lateReplies = 0
	t.lastRTT = 0
	t.lastTTL = 0
	t.lastDSCP = 0
	t.lastLossTime = time.Time{}
	t.lastError = ""
	t.rtt = rttAccumulator{}
	t.historyTimes = nil
	policy, configured := t.health.policy, t.health.configured
	t.health = targetHealth{policy: policy, configured: configured}
	t.jitter = 0
	if t.mtrStats != nil {
		t.mtrStats.Reset()
	}
}

// displayNameLocked returns the label shown to the user: the display name
// when one was set, otherwise the configured host. The label may change
// while probes keep using their original transport address.
func (t *TargetStats) displayNameLocked() string {
	if t.displayName != "" {
		return t.displayName
	}
	return t.host
}

func (t *TargetStats) SetDisplayName(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if name == t.displayNameLocked() {
		return
	}
	t.displayName = name
	t.recordEventLocked("renamed", "Display name changed to "+name)
	bumpGeneration()
}

func (t *TargetStats) SetResolveAddress(address string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resolveAddress = address
}

func (t *TargetStats) ResolveAddress() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.resolveAddress != "" {
		return t.resolveAddress
	}
	return t.host
}

// DisplayName returns the target's display label (matching TargetView.Host)
// without copying RTT history or auxiliary monitor views.
func (t *TargetStats) DisplayName() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.displayNameLocked()
}

// CurrentIP returns the target's currently resolved IP address without
// copying RTT history or auxiliary monitor views.
func (t *TargetStats) CurrentIP() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ip
}

// HostAndIP returns the target's display label and currently resolved IP
// under a single read lock without copying RTT history or auxiliary views.
func (t *TargetStats) HostAndIP() (host, ip string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.displayNameLocked(), t.ip
}

// PortCount returns the number of port check results without copying them.
func (t *TargetStats) PortCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.portResults)
}

// EffectiveAddress returns the resolved IP when available, falling back to
// ResolveAddress() so auxiliary probes (traceroute, MTR) can start before or
// after the first DNS resolution completes without calling GetView().
func (t *TargetStats) EffectiveAddress() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.ip != "" {
		return t.ip
	}
	if t.resolveAddress != "" {
		return t.resolveAddress
	}
	return t.host
}
