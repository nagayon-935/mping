package stats

import "sync/atomic"

// generation is a process-wide monotonic counter bumped by every mutation
// across TargetStats, MTRStats, PortCheckResult, and HTTPCheckResult (see
// bumpGeneration's call sites). The UI's refresh loop compares consecutive
// reads of Generation() to decide whether anything actually changed since
// the last tick, without needing a separate dirty flag threaded through
// every producer (ping worker, MTR engine, port/HTTP checkers, traceroute,
// ASN lookup, PMTU discovery).
//
// A single shared counter (rather than one per TargetStats) is deliberate:
// it can't miss a source the way summing several independently-added
// per-object counters could, since every mutator that changes anything
// UI-visible already funnels through this file's bumpGeneration call.
var generation atomic.Uint64

// bumpGeneration records that something UI-visible changed. Called by
// mutators, not by callers of this package.
func bumpGeneration() {
	generation.Add(1)
}

// Generation returns the current value of the process-wide dirty counter.
// Two reads comparing equal mean nothing tracked by bumpGeneration changed
// in between.
func Generation() uint64 {
	return generation.Load()
}
