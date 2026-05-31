package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestThresholds_Validate(t *testing.T) {
	tests := []struct {
		name    string
		th      Thresholds
		wantErr bool
	}{
		{"defaults", DefaultThresholds(), false},
		{
			name: "rtt warn >= crit",
			th: Thresholds{
				RTTWarn: 200 * time.Millisecond, RTTCrit: 200 * time.Millisecond,
				JitterWarn: 10 * time.Millisecond, JitterCrit: 50 * time.Millisecond,
				LossWarn: 20, LossCrit: 80,
			},
			wantErr: true,
		},
		{
			name: "jitter warn > crit",
			th: Thresholds{
				RTTWarn: 50 * time.Millisecond, RTTCrit: 200 * time.Millisecond,
				JitterWarn: 60 * time.Millisecond, JitterCrit: 50 * time.Millisecond,
				LossWarn: 20, LossCrit: 80,
			},
			wantErr: true,
		},
		{
			name: "loss out of range",
			th: Thresholds{
				RTTWarn: 50 * time.Millisecond, RTTCrit: 200 * time.Millisecond,
				JitterWarn: 10 * time.Millisecond, JitterCrit: 50 * time.Millisecond,
				LossWarn: 20, LossCrit: 120,
			},
			wantErr: true,
		},
		{
			name: "loss warn >= crit",
			th: Thresholds{
				RTTWarn: 50 * time.Millisecond, RTTCrit: 200 * time.Millisecond,
				JitterWarn: 10 * time.Millisecond, JitterCrit: 50 * time.Millisecond,
				LossWarn: 80, LossCrit: 80,
			},
			wantErr: true,
		},
		{
			name: "zero rtt",
			th: Thresholds{
				RTTWarn: 0, RTTCrit: 200 * time.Millisecond,
				JitterWarn: 10 * time.Millisecond, JitterCrit: 50 * time.Millisecond,
				LossWarn: 20, LossCrit: 80,
			},
			wantErr: true,
		},
		{
			name: "custom valid",
			th: Thresholds{
				RTTWarn: 30 * time.Millisecond, RTTCrit: 100 * time.Millisecond,
				JitterWarn: 5 * time.Millisecond, JitterCrit: 20 * time.Millisecond,
				LossWarn: 10, LossCrit: 50,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.th.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// withThresholds temporarily swaps activeThresholds for a test and restores it.
func withThresholds(th Thresholds, fn func()) {
	prev := activeThresholds
	activeThresholds = th
	defer func() { activeThresholds = prev }()
	fn()
}

func TestColorFns_UseActiveThresholds(t *testing.T) {
	custom := Thresholds{
		RTTWarn: 10 * time.Millisecond, RTTCrit: 20 * time.Millisecond,
		JitterWarn: 2 * time.Millisecond, JitterCrit: 4 * time.Millisecond,
		LossWarn: 5, LossCrit: 10,
	}
	withThresholds(custom, func() {
		// RTT: 15ms is above warn (10) but below crit (20) → orange
		if c := rttColorForRTT(15 * time.Millisecond); c != tcell.ColorOrange {
			t.Errorf("rtt 15ms: got %v, want orange (warn=10ms,crit=20ms)", c)
		}
		if c := rttColorForRTT(25 * time.Millisecond); c != vividRed {
			t.Errorf("rtt 25ms: got %v, want red (crit=20ms)", c)
		}
		// Jitter: 3ms → orange, 5ms → red
		if c := jitterColorForJitter(3 * time.Millisecond); c != tcell.ColorOrange {
			t.Errorf("jitter 3ms: got %v, want orange (warn=2ms,crit=4ms)", c)
		}
		if c := jitterColorForJitter(5 * time.Millisecond); c != vividRed {
			t.Errorf("jitter 5ms: got %v, want red (crit=4ms)", c)
		}
		// Loss: 7% → orange, 12% → red under custom thresholds
		if c := lossColorForRate(7); c != tcell.ColorOrange {
			t.Errorf("loss 7%%: got %v, want orange (warn=5,crit=10)", c)
		}
		if c := lossColorForRate(12); c != vividRed {
			t.Errorf("loss 12%%: got %v, want red (crit=10)", c)
		}
	})
}
