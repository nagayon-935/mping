package ui

import (
	"fmt"
	"time"
)

// Thresholds holds the warn/crit boundaries used to colour-code statistics and
// to trigger alert log entries. Values at or below Warn render green, above
// Warn up to Crit render orange, and above Crit render red.
type Thresholds struct {
	RTTWarn    time.Duration
	RTTCrit    time.Duration
	JitterWarn time.Duration
	JitterCrit time.Duration
	LossWarn   float64 // percent (0–100)
	LossCrit   float64 // percent (0–100)
}

// DefaultThresholds returns the built-in default colour thresholds.
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
	if t.LossWarn < 0 || t.LossWarn > 100 || t.LossCrit < 0 || t.LossCrit > 100 {
		return fmt.Errorf("loss thresholds must be between 0 and 100")
	}
	if t.LossWarn >= t.LossCrit {
		return fmt.Errorf("loss-warn (%.1f) must be less than loss-crit (%.1f)", t.LossWarn, t.LossCrit)
	}
	return nil
}

// activeThresholds holds the thresholds currently in effect for rendering.
// It defaults to DefaultThresholds and is overwritten once at Run() startup
// when RunOptions.Thresholds is provided. The TUI renders on a single
// goroutine, so a package-level value needs no synchronisation.
var activeThresholds = DefaultThresholds()
