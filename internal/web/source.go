// Package web serves a read-only, loopback-only browser view of mping's live
// statistics: a JSON API plus a server-sent-events stream that pushes a fresh
// snapshot whenever stats.Generation() moves.
package web

import (
	"sync/atomic"

	"github.com/nagayon-935/mping/internal/stats"
)

// Provider exposes one run-loop iteration's live state. cmd/main builds a
// new one each time the loop is re-entered on a YAML reload.
type Provider interface {
	Targets() []*stats.TargetStats
	HTTPResults() []*stats.HTTPCheckResult
	Meta() Meta
}

// Meta is the run configuration the browser needs to render a snapshot the
// same way the TUI does (which columns exist, colour thresholds, grouping).
type Meta struct {
	IntervalMs float64    `json:"interval_ms"`
	Features   Features   `json:"features"`
	Thresholds Thresholds `json:"thresholds"`
	Groups     []Group    `json:"groups,omitempty"`
}

// Features mirrors the TUI's feature toggles.
type Features struct {
	Traceroute bool `json:"traceroute"`
	MTR        bool `json:"mtr"`
	Port       bool `json:"port"`
	HTTP       bool `json:"http"`
	ASN        bool `json:"asn"`
	PTR        bool `json:"ptr"`
	DSCP       bool `json:"dscp"`
}

// Thresholds are the warn/crit colour boundaries, in JSON-friendly units.
type Thresholds struct {
	RTTWarnMs    float64 `json:"rtt_warn_ms"`
	RTTCritMs    float64 `json:"rtt_crit_ms"`
	JitterWarnMs float64 `json:"jitter_warn_ms"`
	JitterCritMs float64 `json:"jitter_crit_ms"`
	LossWarnPct  float64 `json:"loss_warn_pct"`
	LossCritPct  float64 `json:"loss_crit_pct"`
}

// Group is a named set of targets, identified by stats.TargetStats.ID so the
// grouping stays valid while hosts are added or removed at runtime.
type Group struct {
	Name      string   `json:"name"`
	TargetIDs []uint64 `json:"target_ids"`
}

// State is the run-loop phase reported to the browser.
type State string

const (
	// StateStarting: no iteration has published a provider yet.
	StateStarting State = "starting"
	// StateRunning: the current provider's iteration is live.
	StateRunning State = "running"
	// StateReloading: the iteration ended and the run loop is re-entering
	// for a YAML reload or host add/delete; numbers are frozen meanwhile.
	StateReloading State = "reloading"
	// StateStopped: mping is exiting; the numbers are final.
	StateStopped State = "stopped"
)

type sourceState struct {
	provider Provider
	state    State
	version  uint64
}

// Source is the swap point between the long-lived HTTP server and the
// per-iteration Provider. The zero value has no provider and is ready to
// use; it is safe for concurrent use.
type Source struct {
	cur atomic.Pointer[sourceState]
}

// NewSource returns a Source with no provider; API calls answer 503 until
// Set is called.
func NewSource() *Source { return &Source{} }

// Set installs p as the live provider and marks the source running.
func (s *Source) Set(p Provider) {
	s.update(func(sourceState) sourceState {
		return sourceState{provider: p, state: StateRunning}
	})
}

// MarkReloading flags that the current iteration ended and another one is
// about to start. The last provider stays readable so the browser keeps
// showing its final numbers.
func (s *Source) MarkReloading() { s.mark(StateReloading) }

// MarkStopped flags that mping is exiting. The last provider stays readable.
func (s *Source) MarkStopped() { s.mark(StateStopped) }

// State reports the current phase.
func (s *Source) State() State {
	_, st, _ := s.load()
	return st
}

func (s *Source) mark(state State) {
	s.update(func(st sourceState) sourceState {
		st.state = state
		return st
	})
}

func (s *Source) update(f func(sourceState) sourceState) {
	for {
		old := s.cur.Load()
		var prev sourceState
		if old != nil {
			prev = *old
		}
		next := f(prev)
		next.version = prev.version + 1
		if s.cur.CompareAndSwap(old, &next) {
			return
		}
	}
}

// load returns the current provider (nil before the first Set), the phase,
// and a version that changes on every transition.
func (s *Source) load() (Provider, State, uint64) {
	st := s.cur.Load()
	if st == nil || st.state == "" {
		var version uint64
		if st != nil {
			version = st.version
		}
		return nil, StateStarting, version
	}
	return st.provider, st.state, st.version
}
