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

// TargetStats holds the statistics for a single ping target.
var targetIDs atomic.Uint64

type IPChange struct {
	At time.Time `json:"at"`
	IP string    `json:"ip"`
}

type TargetStats struct {
	ID              uint64 // Immutable identity; never reused within this process.
	StartedAt       time.Time
	WindowStartedAt time.Time
	DSCP            string
	IPHistory       []IPChange
	IPChanges       int
	Cancelled       int

	Host             string
	IP               string
	ASN              string
	Country          string
	Org              string
	PTR              string // reverse DNS (PTR) hostname for IP, when --ptr is enabled
	DNSServer        string
	IfaceMTU         int
	PMTU             int
	PMTUBottleneckIP string
	TraceHops        []string
	PortResults      []*PortCheckResult
	mtrStats         *MTRStats
	Sent             int
	Recv             int
	Loss             int
	// Duplicates counts replies that repeat a seq already resolved (either
	// by a successful reply or an ICMP error) -- a network-level dup
	// (routing loop, L2 duplication, NAT/load-balancer anomaly), not a
	// second successful probe. Never folded into Recv: doing so would
	// understate the real loss rate, exactly the reason Apple's ping.c
	// decrements nreceived when it detects a dup.
	Duplicates int
	// LateReplies counts replies that arrive for a seq whose `unacked`
	// entry was already swept as a timeout (and so already counted in
	// Loss). Tracked separately so a target that's simply slow can be told
	// apart from one that's truly dropping packets -- mirrors ping.c's
	// -W/nrcvtimeout. The Loss already recorded for that probe is left
	// standing; a late reply never decrements it.
	LateReplies int
	LastRTT     time.Duration
	LastTTL     int
	// LastDSCP is the TOS (IPv4) / TrafficClass (IPv6) byte observed on the
	// most recent successful reply's own IP header — i.e. what DSCP marking
	// actually arrived, after any re-marking or bleaching along the return
	// path. Zero both as the pre-first-reply default and when the platform
	// didn't supply a control message; same ambiguity as LastTTL's zero
	// value, and for the same reason (see pinger.Reply.DSCP).
	LastDSCP     int
	LastLossTime time.Time
	LastError    string

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
		Host:             host,
		ID:               targetIDs.Add(1),
		StartedAt:        now,
		WindowStartedAt:  now,
		measurementState: "running",
	}
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
	if t.IP != ip {
		t.recordEventLocked("dns", "IP: "+t.IP+" → "+ip)
		if t.IP != "" {
			t.IPChanges++
		}
		t.IPHistory = append(t.IPHistory, IPChange{At: time.Now(), IP: ip})
		if len(t.IPHistory) > 64 {
			t.IPHistory = append([]IPChange(nil), t.IPHistory[len(t.IPHistory)-64:]...)
		}
	}
	t.IP = ip
}

func (t *TargetStats) SetASNInfo(number, country, org string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ASN = number
	t.Country = country
	t.Org = org
}

// SetPTR records the resolved PTR (reverse DNS) hostname for t's IP.
func (t *TargetStats) SetPTR(name string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.PTR = name
}

func (t *TargetStats) SetDNSServer(dnsServer string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.DNSServer = dnsServer
}

func (t *TargetStats) SetTraceHops(hops []string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.TraceHops = make([]string, len(hops))
	copy(t.TraceHops, hops)
}

func (t *TargetStats) SetIfaceMTU(mtu int) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.IfaceMTU = mtu
}

func (t *TargetStats) SetPMTU(pmtu int) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.PMTU = pmtu
}

func (t *TargetStats) SetPMTUBottleneckIP(ip string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.PMTUBottleneckIP = ip
}

// SetPortResults replaces the port check results slice atomically.
func (t *TargetStats) SetPortResults(results []*PortCheckResult) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.PortResults = results
}

func (t *TargetStats) IncSent() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Sent++
}

func (t *TargetStats) OnSuccess(rtt time.Duration, ttl int) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onSuccessLocked(rtt, ttl)
}

func (t *TargetStats) onSuccessLocked(rtt time.Duration, ttl int) {
	if t.LastError != "" {
		t.recordEventLocked("recovery", "Ping replies resumed")
	}
	// RFC 1889 Jitter Calculation: J = J + (|D| - J) / 16
	if t.Recv > 0 {
		t.jitter = updateJitter(t.jitter, rtt, t.LastRTT)
	}

	t.Recv++
	t.LastRTT = rtt
	t.LastTTL = ttl
	t.rtt.record(rtt)
	now := time.Now()
	t.appendHistoryLocked(rtt, now)
	t.recordOutcomeLocked(now, true)

	t.LastError = ""
}

// SetLastDSCP records the DSCP-derived TOS/TrafficClass byte observed on the
// most recent successful reply. Called separately from OnSuccess (rather
// than as an extra OnSuccess parameter) to avoid touching OnSuccess's
// existing call sites across cmd/main and internal/ui's test suites.
func (t *TargetStats) SetLastDSCP(dscp int) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.LastDSCP = dscp
}

// OnDuplicate records a reply that repeats a seq already resolved. See the
// Duplicates field's doc for why this must never be folded into Recv.
func (t *TargetStats) OnDuplicate() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Duplicates++
}

// OnLateReply records a reply that arrived after its probe's `unacked`
// entry was already swept as a timeout. See the LateReplies field's doc for
// why the Loss already recorded for that probe is left untouched here.
func (t *TargetStats) OnLateReply() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.LateReplies++
}

func (t *TargetStats) OnFailure(reason string) {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFailureLocked(reason)
}

func (t *TargetStats) onFailureLocked(reason string) {
	t.Loss++
	t.LastLossTime = time.Now()
	t.LastError = reason
	t.recordEventLocked("ping", reason)
	t.appendHistoryLocked(0, t.LastLossTime)
	t.recordOutcomeLocked(t.LastLossTime, false)
}

func (t *TargetStats) Reset() {
	defer bumpGeneration()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.probeEpoch++
	t.WindowStartedAt = time.Now()
	t.recordEventLocked("reset", "Statistics reset")
	t.Sent = 0
	t.Cancelled = 0
	t.Recv = 0
	t.Loss = 0
	t.Duplicates = 0
	t.LateReplies = 0
	t.LastRTT = 0
	t.LastTTL = 0
	t.LastDSCP = 0
	t.LastLossTime = time.Time{}
	t.LastError = ""
	t.rtt = rttAccumulator{}
	t.historyTimes = nil
	policy, configured := t.health.policy, t.health.configured
	t.health = targetHealth{policy: policy, configured: configured}
	t.jitter = 0
	if t.mtrStats != nil {
		t.mtrStats.Reset()
	}
}

// Host stays immutable for older callers. The display label may be changed
// while probes keep using their original transport address.
func (t *TargetStats) displayNameLocked() string {
	if t.displayName != "" {
		return t.displayName
	}
	return t.Host
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
	return t.Host
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
	return t.IP
}

// HostAndIP returns the target's display label and currently resolved IP
// under a single read lock without copying RTT history or auxiliary views.
func (t *TargetStats) HostAndIP() (host, ip string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.displayNameLocked(), t.IP
}

// EffectiveAddress returns the resolved IP when available, falling back to
// ResolveAddress() so auxiliary probes (traceroute, MTR) can start before or
// after the first DNS resolution completes without calling GetView().
func (t *TargetStats) EffectiveAddress() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.IP != "" {
		return t.IP
	}
	if t.resolveAddress != "" {
		return t.resolveAddress
	}
	return t.Host
}
