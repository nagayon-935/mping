package stats

import (
	"math"
	"testing"
	"time"
)

func TestThresholds_Validate(t *testing.T) {
	valid := DefaultThresholds()
	tests := []struct {
		name    string
		mutate  func(*Thresholds)
		wantErr bool
	}{
		{"defaults", func(*Thresholds) {}, false},
		{"rtt warn >= crit", func(th *Thresholds) { th.RTTWarn = th.RTTCrit }, true},
		{"jitter warn > crit", func(th *Thresholds) { th.JitterWarn = th.JitterCrit + time.Millisecond }, true},
		{"loss out of range", func(th *Thresholds) { th.LossCrit = 120 }, true},
		{"loss warn >= crit", func(th *Thresholds) { th.LossWarn = th.LossCrit }, true},
		{"loss NaN", func(th *Thresholds) { th.LossWarn = math.NaN() }, true},
		{"zero rtt", func(th *Thresholds) { th.RTTWarn = 0 }, true},
		{"custom valid", func(th *Thresholds) {
			*th = Thresholds{RTTWarn: 30 * time.Millisecond, RTTCrit: 100 * time.Millisecond,
				JitterWarn: 5 * time.Millisecond, JitterCrit: 20 * time.Millisecond, LossWarn: 10, LossCrit: 50}
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := valid
			tt.mutate(&th)
			if err := th.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestThresholds_Levels(t *testing.T) {
	th := DefaultThresholds() // RTT 50/200ms, jitter 10/50ms, loss 20/80%
	ms := time.Millisecond
	tests := []struct {
		name string
		got  Level
		want Level
	}{
		{"rtt no sample", th.RTT(0), LevelNone},
		{"rtt at warn is ok", th.RTT(50 * ms), LevelOK},
		{"rtt above warn", th.RTT(51 * ms), LevelWarn},
		{"rtt at crit is warn", th.RTT(200 * ms), LevelWarn},
		{"rtt above crit", th.RTT(201 * ms), LevelCrit},
		{"jitter no sample", th.Jitter(0), LevelNone},
		{"jitter above warn", th.Jitter(11 * ms), LevelWarn},
		{"jitter above crit", th.Jitter(51 * ms), LevelCrit},
		{"loss zero", th.Loss(0), LevelOK},
		{"loss at warn is ok", th.Loss(20), LevelOK},
		{"loss above warn", th.Loss(20.1), LevelWarn},
		{"loss above crit", th.Loss(80.1), LevelCrit},
		{"hop no loss", th.HopLoss(0), LevelOK},
		{"hop any loss warns", th.HopLoss(0.1), LevelWarn},
		{"hop crit is inclusive", th.HopLoss(80), LevelCrit},
		{"port open", PortStatusLevel("Open"), LevelOK},
		{"port closed", PortStatusLevel("Closed"), LevelCrit},
		{"port filtered", PortStatusLevel("Filtered"), LevelWarn},
		{"port open|filtered", PortStatusLevel("Open|Filtered"), LevelWarn},
		{"port checking", PortStatusLevel("Checking..."), LevelPending},
		{"http up", HTTPStatusLevel("Up"), LevelOK},
		{"http down", HTTPStatusLevel("Down"), LevelCrit},
		{"http error", HTTPStatusLevel("Error"), LevelCrit},
		{"http checking", HTTPStatusLevel("Checking..."), LevelPending},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestLossRatioIgnoresInFlightProbes(t *testing.T) {
	if got := LossRatio(6, 4); got != 40 {
		t.Errorf("LossRatio(6, 4) = %v, want 40", got)
	}
	if got := LossRatio(0, 0); got != 0 {
		t.Errorf("LossRatio(0, 0) = %v, want 0", got)
	}
}

func TestViewLevelsFollowTheTargetsThresholds(t *testing.T) {
	target := NewTargetStats("example.com")
	if v := target.GetView(); v.Levels != (Levels{Loss: LevelNone, LastRTT: LevelNone, Jitter: LevelNone}) {
		t.Fatalf("levels before any probe = %+v, want all none", v.Levels)
	}

	target.IncSent()
	target.OnSuccess(30*time.Millisecond, 64)
	if got := target.GetView().Levels.LastRTT; got != LevelOK {
		t.Fatalf("30ms under default thresholds = %q, want ok", got)
	}

	policy := DefaultHealthPolicy()
	policy.RTTWarn, policy.RTTCrit = 10*time.Millisecond, 20*time.Millisecond
	target.ConfigureHealth(policy)
	v := target.GetView()
	if v.Thresholds != policy.Thresholds {
		t.Fatalf("view thresholds = %+v, want the configured %+v", v.Thresholds, policy.Thresholds)
	}
	if v.Levels.LastRTT != LevelCrit || v.Levels.Loss != LevelOK {
		t.Fatalf("levels under custom thresholds = %+v, want last_rtt crit and loss ok", v.Levels)
	}
}
