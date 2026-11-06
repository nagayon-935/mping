package stats

import "testing"

func TestPathMTUConfidenceAndDestination(t *testing.T) {
	target := NewTargetStats("router")
	target.SetPathMTU(PathMTUInfo{IP: "2001:db8::1", MTU: 1450, Payload: 1402, Source: "os", Status: "estimated"})
	target.SetIP("2001:db8::1")
	view := target.GetView()
	if view.PathMTU != 1450 || view.PMTU != 1402 || view.PMTUSource != "os" || view.PMTUStatus != "estimated" {
		t.Fatalf("view lost PMTU confidence: %+v", view)
	}
	snapshot := BuildSnapshot([]*TargetStats{target}, nil).Targets[0]
	if snapshot.PathMTU != 1450 || snapshot.PMTU != 1402 || snapshot.PMTUStatus != "estimated" || snapshot.PMTUSource != "os" {
		t.Fatalf("export lost PMTU confidence: %+v", snapshot)
	}
	target.SetIP("2001:db8::2")
	view = target.GetView()
	if view.PathMTU != 0 || view.PMTU != 0 || view.PMTUStatus != "" || view.PMTUSource != "" {
		t.Fatalf("changed IP retained old path's PMTU: %+v", view)
	}
}
