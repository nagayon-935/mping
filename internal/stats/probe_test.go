package stats

import (
	"testing"
	"time"
)

func TestProbeResetInvalidatesAllPreviousUpdates(t *testing.T) {
	target := NewTargetStats("example.com")
	old := target.NewProbe()
	old.IncSent()
	old.OnSuccess(time.Millisecond, 64, 184)
	target.Reset()
	updates := []bool{old.IncSent(), old.OnSuccess(time.Second, 1, 1), old.OnFailure("timeout"), old.OnDuplicate(), old.OnLateReply()}
	for _, accepted := range updates {
		if accepted {
			t.Fatal("accepted an old statistics window")
		}
	}
	fresh := target.NewProbe()
	if !fresh.IncSent() || !fresh.OnSuccess(2*time.Millisecond, 63, 8) {
		t.Fatal("new window did not accept probe")
	}
	v := target.GetView()
	if v.Sent != 1 || v.Recv != 1 || v.Loss != 0 || v.Duplicates != 0 || v.LateReplies != 0 || v.LastDSCP != 8 || len(v.History) != 1 {
		t.Fatalf("wrong reset window: %+v", v)
	}
}
