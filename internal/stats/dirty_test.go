package stats

import (
	"testing"
	"time"
)

// TestGeneration_AdvancesOnMutation is a smoke test covering the P7
// dirty-redraw-gate's data source: every mutator the UI's refresh loop
// relies on (ping, MTR, port, HTTP) must bump the shared generation
// counter, or that source's pane would silently stop refreshing under the
// gate.
func TestGeneration_AdvancesOnMutation(t *testing.T) {
	tgt := NewTargetStats("example.com")

	mustAdvance := func(name string, mutate func()) {
		t.Helper()
		before := Generation()
		mutate()
		if after := Generation(); after == before {
			t.Errorf("%s did not advance Generation() (stayed at %d)", name, before)
		}
	}

	mustAdvance("IncSent", func() { tgt.IncSent() })
	mustAdvance("OnSuccess", func() { tgt.OnSuccess(10*time.Millisecond, 64) })
	mustAdvance("OnFailure", func() { tgt.OnFailure("Timeout") })
	mustAdvance("OnDuplicate", func() { tgt.OnDuplicate() })
	mustAdvance("OnLateReply", func() { tgt.OnLateReply() })
	mustAdvance("SetIP", func() { tgt.SetIP("1.1.1.1") })
	mustAdvance("SetASNInfo", func() { tgt.SetASNInfo("AS15169", "US", "Google LLC") })
	mustAdvance("SetDNSServer", func() { tgt.SetDNSServer("8.8.8.8") })
	mustAdvance("SetTraceHops", func() { tgt.SetTraceHops([]string{"10.0.0.1"}) })
	mustAdvance("SetIfaceMTU", func() { tgt.SetIfaceMTU(1500) })
	mustAdvance("SetPMTU", func() { tgt.SetPMTU(1400) })
	mustAdvance("SetPMTUBottleneckIP", func() { tgt.SetPMTUBottleneckIP("10.0.0.1") })
	mustAdvance("SetPortResults", func() { tgt.SetPortResults(nil) })
	mustAdvance("TargetStats.Reset", func() { tgt.Reset() })

	port := &PortCheckResult{Port: 443, Protocol: "tcp"}
	mustAdvance("PortCheckResult.SetResult", func() { port.SetResult("Open", 5*time.Millisecond) })

	http := NewHTTPCheckResult("https://example.com")
	mustAdvance("HTTPCheckResult.SetResult", func() { http.SetResult(200, 5*time.Millisecond, nil) })

	mtrStats := NewMTRStats()
	mustAdvance("MTRStats.EnsureLen", func() { mtrStats.EnsureLen(3) })
	mustAdvance("MTRStats.SetIP", func() { mtrStats.SetIP(1, "10.0.0.1", "", "", "") })
	mustAdvance("MTRStats.RecordReply", func() { mtrStats.RecordReply(1, "10.0.0.1", "", "", "", 5*time.Millisecond) })
	mustAdvance("MTRStats.RecordLoss", func() { mtrStats.RecordLoss(2) })
	mustAdvance("MTRStats.TruncateLen", func() { mtrStats.TruncateLen(1) })
	mustAdvance("MTRStats.CheckFlap", func() { mtrStats.CheckFlap([]string{"different"}, time.Now()) })
	mustAdvance("MTRStats.Reset", func() { mtrStats.Reset() })
}
