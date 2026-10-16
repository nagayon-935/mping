package stats

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// HealthPolicy is shared by every presentation. Lifetime counters are never
// used to decide whether a previously healthy target is responding now.
type HealthPolicy struct {
	Failures                                 int
	Recoveries                               int
	Window                                   time.Duration
	RTTWarn, RTTCrit, JitterWarn, JitterCrit time.Duration
	LossWarn, LossCrit                       float64
}

func DefaultHealthPolicy() HealthPolicy {
	return HealthPolicy{Failures: 3, Recoveries: 2, Window: time.Minute,
		RTTWarn: 50 * time.Millisecond, RTTCrit: 200 * time.Millisecond,
		JitterWarn: 10 * time.Millisecond, JitterCrit: 50 * time.Millisecond,
		LossWarn: 20, LossCrit: 80}
}

func (p HealthPolicy) Validate() error {
	if p.Failures < 1 || p.Failures > 10000 || p.Recoveries < 1 || p.Recoveries > 10000 {
		return fmt.Errorf("health failure/recovery thresholds must be between 1 and 10000")
	}
	if p.Window < time.Second || p.Window > time.Hour {
		return fmt.Errorf("health window must be between 1s and 1h")
	}
	if p.RTTWarn <= 0 || p.RTTCrit <= p.RTTWarn || p.JitterWarn <= 0 || p.JitterCrit <= p.JitterWarn ||
		math.IsNaN(p.LossWarn) || math.IsNaN(p.LossCrit) || p.LossWarn < 0 || p.LossCrit > 100 || p.LossCrit <= p.LossWarn {
		return fmt.Errorf("health thresholds must be positive, ordered warn < crit; loss must be between 0 and 100")
	}
	return nil
}

type Health struct {
	Unavailable          bool      `json:"unavailable"`
	Configured           bool      `json:"configured"`
	Status               string    `json:"status"`
	Reason               string    `json:"reason"`
	Since                time.Time `json:"since,omitzero"`
	LastProbeAt          time.Time `json:"last_probe_at,omitzero"`
	LastReplyAt          time.Time `json:"last_reply_at,omitzero"`
	ConsecutiveFailures  int       `json:"consecutive_failures"`
	ConsecutiveSuccesses int       `json:"consecutive_successes"`
	RecentCompleted      int       `json:"recent_completed"`
	RecentLoss           int       `json:"recent_loss"`
	RecentLossPct        float64   `json:"recent_loss_pct"`
	WindowSeconds        float64   `json:"window_seconds"`
	EvaluatedAt          time.Time `json:"evaluated_at,omitzero"`
}

type outcomeBucket struct {
	second          int64
	replies, losses int
}
type targetHealth struct {
	policy                      HealthPolicy
	configured                  bool
	status, reason              string
	since, lastProbe, lastReply time.Time
	failures, successes         int
	down                        bool
	buckets                     []outcomeBucket
	recentReceived, recentLost  int
	evaluatedAt                 time.Time
}

// ConfigureHealth changes judgment rules without discarding measurements.
// It is also the opt-in for common backend health events and failure folding.
func (t *TargetStats) ConfigureHealth(p HealthPolicy) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := p.Validate(); err != nil {
		p = DefaultHealthPolicy()
	}
	if t.health.policy.Window != p.Window {
		t.health.buckets = nil
	}
	t.health.policy = p
	t.health.configured = true
	t.evaluateHealthLocked(time.Now(), t.health.configured)
	bumpGeneration()
}

func (t *TargetStats) healthPolicyLocked() HealthPolicy {
	if t.health.policy.Failures == 0 {
		return DefaultHealthPolicy()
	}
	return t.health.policy
}

func (t *TargetStats) recentHealthLocked(now time.Time) (received, lost int) {
	p := t.healthPolicyLocked()
	for _, b := range t.health.buckets {
		if b.second > now.Add(-p.Window).Unix() && b.second <= now.Unix() {
			received += b.replies
			lost += b.losses
		}
	}
	return
}

func (t *TargetStats) recordOutcomeLocked(now time.Time, success bool) {
	h := &t.health
	p := t.healthPolicyLocked()
	n := int(p.Window/time.Second) + 2
	if len(h.buckets) != n {
		h.buckets = make([]outcomeBucket, n)
	}
	second := now.Unix()
	b := &h.buckets[second%int64(n)]
	if b.second != second {
		*b = outcomeBucket{second: second}
	}
	h.lastProbe = now
	if success {
		b.replies++
		h.lastReply = now
		h.successes++
		h.failures = 0
	} else {
		b.losses++
		h.failures++
		h.successes = 0
	}
	t.evaluateHealthLocked(now, true)
}

func (t *TargetStats) evaluateHealthLocked(now time.Time, emit bool) {
	h := &t.health
	p := t.healthPolicyLocked()
	h.recentReceived, h.recentLost = t.recentHealthLocked(now)
	h.evaluatedAt = now
	status, reasons := "ok", []string{}
	if h.lastProbe.IsZero() {
		status, reasons = "pending", []string{"Waiting for a completed probe"}
	} else {
		if h.failures >= p.Failures {
			h.down = true
		}
		if h.down && h.successes >= p.Recoveries {
			h.down = false
		}
		if h.down {
			status = "crit"
			if h.successes > 0 {
				reasons = append(reasons, fmt.Sprintf("Recovering (%d/%d replies)", h.successes, p.Recoveries))
			} else {
				reasons = append(reasons, fmt.Sprintf("%d consecutive failures", h.failures))
			}
		} else {
			received, lost := h.recentReceived, h.recentLost
			rate := 0.0
			if received+lost > 0 {
				rate = 100 * float64(lost) / float64(received+lost)
			}
			checks := []struct {
				name              string
				value, warn, crit float64
				unit              string
			}{
				{"Recent loss", rate, p.LossWarn, p.LossCrit, "%"},
				{"RTT", float64(t.LastRTT) / float64(time.Millisecond), float64(p.RTTWarn) / float64(time.Millisecond), float64(p.RTTCrit) / float64(time.Millisecond), "ms"},
				{"Jitter", float64(t.jitter) / float64(time.Millisecond), float64(p.JitterWarn) / float64(time.Millisecond), float64(p.JitterCrit) / float64(time.Millisecond), "ms"},
			}
			for _, c := range checks {
				if c.name == "Recent loss" && received+lost < p.Failures {
					continue
				}
				if c.value > c.crit {
					status = "crit"
					reasons = append(reasons, fmt.Sprintf("%s %.1f%s > %.1f%s", c.name, c.value, c.unit, c.crit, c.unit))
				} else if c.value > c.warn {
					if status != "crit" {
						status = "warn"
					}
					reasons = append(reasons, fmt.Sprintf("%s %.1f%s > %.1f%s", c.name, c.value, c.unit, c.warn, c.unit))
				}
			}
			if h.failures > 0 && len(reasons) == 0 {
				reasons = append(reasons, fmt.Sprintf("Waiting for reply (%d/%d failures)", h.failures, p.Failures))
				status = "warn"
			}
		}
	}
	if len(reasons) == 0 {
		reasons = []string{"Within thresholds"}
	}
	reason := strings.Join(reasons, " · ")
	changed := h.status != "" && status != h.status
	if h.status != status {
		h.since = now
	}
	h.status, h.reason = status, reason
	if emit && changed && h.configured {
		t.recordEventLocked("health", fmt.Sprintf("%s: %s", status, reason))
	}
}

func (t *TargetStats) healthViewLocked() Health {
	h := &t.health
	// These are the inputs of the last judgment, not a differently-aged
	// window recomputed by a reader while measurements are paused.
	received, lost := h.recentReceived, h.recentLost
	rate := 0.0
	if received+lost > 0 {
		rate = 100 * float64(lost) / float64(received+lost)
	}
	status, reason := h.status, h.reason
	if status == "" {
		status, reason = "pending", "Waiting for a completed probe"
	}
	return Health{Unavailable: h.down, Configured: h.configured, Status: status, Reason: reason, Since: h.since, LastProbeAt: h.lastProbe, LastReplyAt: h.lastReply,
		ConsecutiveFailures: h.failures, ConsecutiveSuccesses: h.successes, RecentCompleted: received + lost, RecentLoss: lost,
		RecentLossPct: rate, WindowSeconds: t.healthPolicyLocked().Window.Seconds(), EvaluatedAt: h.evaluatedAt}
}

// SetMeasurementState is independent of the HTTP server/session state.
func (t *TargetStats) SetMeasurementState(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.measurementState = state
	bumpGeneration()
}

func (t *TargetStats) MeasurementState() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.measurementState
}

// HistorySample preserves wall time. A nil RTT marks a lost probe.
type HistorySample struct {
	At    time.Time `json:"at"`
	RTTMS *float64  `json:"rtt_ms"`
}

func (t *TargetStats) appendHistoryLocked(rtt time.Duration, at time.Time) {
	t.rtt.appendHistory(rtt, historySize)
	if t.historyTimes == nil {
		t.historyTimes = make([]int64, historySize)
	}
	t.historyTimes[(t.rtt.historyIdx-1)%historySize] = at.UnixNano()
}

func (t *TargetStats) HistorySamples(n int) []HistorySample {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if n > t.rtt.historyLen {
		n = t.rtt.historyLen
	}
	if n < 0 {
		n = 0
	}
	out := make([]HistorySample, 0, n)
	for i := t.rtt.historyLen - n; i < t.rtt.historyLen; i++ {
		idx := (t.rtt.historyIdx - t.rtt.historyLen + i) % historySize
		if t.historyTimes == nil || t.historyTimes[idx] == 0 {
			continue
		}
		sample := HistorySample{At: time.Unix(0, t.historyTimes[idx]).UTC()}
		if d := t.rtt.history[idx]; d > 0 {
			ms := float64(d) / float64(time.Millisecond)
			sample.RTTMS = &ms
		}
		out = append(out, sample)
	}
	return out
}
