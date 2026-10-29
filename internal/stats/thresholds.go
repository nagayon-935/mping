package stats

import (
	"fmt"
	"math"
	"time"
)

// Thresholds holds the warn/crit boundaries every presentation (TUI, web,
// JSON export, health) judges measurements by. A value above Warn is a
// warning and above Crit is critical; the comparisons are strict.
type Thresholds struct {
	RTTWarn    time.Duration
	RTTCrit    time.Duration
	JitterWarn time.Duration
	JitterCrit time.Duration
	LossWarn   float64 // percent (0–100)
	LossCrit   float64 // percent (0–100)
}

// DefaultThresholds returns the built-in default thresholds.
func DefaultThresholds() Thresholds {
	return Thresholds{
		RTTWarn:    50 * time.Millisecond,
		RTTCrit:    200 * time.Millisecond,
		JitterWarn: 10 * time.Millisecond,
		JitterCrit: 50 * time.Millisecond,
		LossWarn:   20.0,
		LossCrit:   80.0,
	}
}

// Validate checks that warn < crit for every metric and that values fall in
// sensible ranges.
func (t Thresholds) Validate() error {
	if t.RTTWarn <= 0 || t.RTTCrit <= 0 {
		return fmt.Errorf("rtt thresholds must be > 0")
	}
	if t.RTTWarn >= t.RTTCrit {
		return fmt.Errorf("rtt-warn (%v) must be less than rtt-crit (%v)", t.RTTWarn, t.RTTCrit)
	}
	if t.JitterWarn <= 0 || t.JitterCrit <= 0 {
		return fmt.Errorf("jitter thresholds must be > 0")
	}
	if t.JitterWarn >= t.JitterCrit {
		return fmt.Errorf("jitter-warn (%v) must be less than jitter-crit (%v)", t.JitterWarn, t.JitterCrit)
	}
	// NaN fails every comparison below, so it has to be rejected explicitly.
	if math.IsNaN(t.LossWarn) || math.IsNaN(t.LossCrit) ||
		t.LossWarn < 0 || t.LossWarn > 100 || t.LossCrit < 0 || t.LossCrit > 100 {
		return fmt.Errorf("loss thresholds must be between 0 and 100")
	}
	if t.LossWarn >= t.LossCrit {
		return fmt.Errorf("loss-warn (%.1f) must be less than loss-crit (%.1f)", t.LossWarn, t.LossCrit)
	}
	return nil
}

// Level is the severity of one measurement, or of a whole target, as shown
// by every presentation. The string values are part of the JSON export.
type Level string

const (
	LevelNone    Level = "none"    // nothing measured yet
	LevelPending Level = "pending" // a check has not produced a result yet
	LevelOK      Level = "ok"
	LevelWarn    Level = "warn"
	LevelCrit    Level = "crit"
)

// duration classifies d; zero or negative means no sample.
func duration(d, warn, crit time.Duration) Level {
	switch {
	case d <= 0:
		return LevelNone
	case d > crit:
		return LevelCrit
	case d > warn:
		return LevelWarn
	default:
		return LevelOK
	}
}

// RTT classifies a round-trip time; zero means no sample.
func (t Thresholds) RTT(d time.Duration) Level { return duration(d, t.RTTWarn, t.RTTCrit) }

// Jitter classifies a jitter value; zero means no sample.
func (t Thresholds) Jitter(d time.Duration) Level { return duration(d, t.JitterWarn, t.JitterCrit) }

// Loss classifies a loss percentage.
func (t Thresholds) Loss(pct float64) Level {
	switch {
	case pct > t.LossCrit:
		return LevelCrit
	case pct > t.LossWarn:
		return LevelWarn
	default:
		return LevelOK
	}
}

// HopLoss classifies an MTR hop's loss percentage. Any loss at a hop is
// worth a look, so it warns from the first lost probe and is critical at or
// above LossCrit.
func (t Thresholds) HopLoss(pct float64) Level {
	switch {
	case pct >= t.LossCrit:
		return LevelCrit
	case pct > 0:
		return LevelWarn
	default:
		return LevelOK
	}
}

// Levels are the per-measurement levels of a target's latest statistics.
type Levels struct {
	// Loss is the lifetime loss ratio over completed probes; LevelNone until
	// the first probe completes.
	Loss    Level `json:"loss"`
	LastRTT Level `json:"last_rtt"`
	Jitter  Level `json:"jitter"`
}

// LossRatio is the percentage of completed probes (replies plus losses) that
// were lost; in-flight probes are not counted. 0 when none has completed.
func LossRatio(recv, loss int) float64 {
	if recv+loss == 0 {
		return 0
	}
	return float64(loss) / float64(recv+loss) * 100
}

func (t Thresholds) levels(recv, loss int, lastRTT, jitter time.Duration) Levels {
	l := Levels{Loss: LevelNone, LastRTT: t.RTT(lastRTT), Jitter: t.Jitter(jitter)}
	if recv+loss > 0 {
		l.Loss = t.Loss(LossRatio(recv, loss))
	}
	return l
}

// PortStatusLevel classifies a port check status.
func PortStatusLevel(status string) Level {
	switch status {
	case "Open":
		return LevelOK
	case "Closed":
		return LevelCrit
	case "Filtered", "Open|Filtered":
		return LevelWarn
	default:
		return LevelPending
	}
}

// HTTPStatusLevel classifies an HTTP check status.
func HTTPStatusLevel(status string) Level {
	switch status {
	case "Up":
		return LevelOK
	case "Down", "Error":
		return LevelCrit
	default:
		return LevelPending
	}
}
