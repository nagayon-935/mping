package ui

import (
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
)

// TestTableColorsFollowTheTargetsThresholds renders a row for a target whose
// health policy carries custom thresholds and checks the cells are coloured
// by those, not by the defaults (30ms RTT is fine under the defaults).
func TestTableColorsFollowTheTargetsThresholds(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.SetIP("192.0.2.1")
	policy := stats.DefaultHealthPolicy()
	policy.Thresholds = stats.Thresholds{
		RTTWarn: 10 * time.Millisecond, RTTCrit: 20 * time.Millisecond,
		JitterWarn: 2 * time.Millisecond, JitterCrit: 4 * time.Millisecond,
		LossWarn: 5, LossCrit: 10,
	}
	target.ConfigureHealth(policy)
	target.IncSent()
	target.OnSuccess(30*time.Millisecond, 64)
	target.IncSent()
	target.OnSuccess(33*time.Millisecond, 64) // jitter 3ms/16: below warn
	for range 2 {
		target.IncSent()
		target.OnFailure("timeout") // loss 2/4 = 50%: above crit
	}

	tr := newTestTableRenderer([]*stats.TargetStats{target}, 200)
	tr.update()

	want := map[string]tcell.Color{"Loss Ratio": vividRed, "RTT": vividRed, "Jitter": tcell.ColorGreen}
	for name, color := range want {
		idx := columnsByName(tr.cols, name)
		if idx < 0 {
			t.Fatalf("column %q missing", name)
		}
		if got := cellForeground(tr.table.GetCell(1, idx)); got != color {
			t.Errorf("%s cell color = %v, want %v", name, got, color)
		}
	}
}
