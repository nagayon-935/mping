package stats

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMTRStats_RecordReplyAndLoss(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(3)

	m.RecordReply(1, "10.0.0.1", "", "", "", 10*time.Millisecond)
	m.RecordReply(1, "10.0.0.1", "", "", "", 20*time.Millisecond)
	m.RecordLoss(1)

	hops := m.View()
	if len(hops) != 3 {
		t.Fatalf("expected 3 hops, got %d", len(hops))
	}

	h := hops[0] // TTL=1
	if h.TTL != 1 {
		t.Errorf("TTL: want 1, got %d", h.TTL)
	}
	if h.IP != "10.0.0.1" {
		t.Errorf("IP: want 10.0.0.1, got %q", h.IP)
	}
	if h.Sent != 3 {
		t.Errorf("Sent: want 3, got %d", h.Sent)
	}
	if h.Recv != 2 {
		t.Errorf("Recv: want 2, got %d", h.Recv)
	}
	wantLoss := 100.0 / 3.0
	if diff := h.LossPct - wantLoss; diff > 0.01 || diff < -0.01 {
		t.Errorf("LossPct: want ~%.2f, got %.2f", wantLoss, h.LossPct)
	}
	if h.MinRTT != 10*time.Millisecond {
		t.Errorf("MinRTT: want 10ms, got %v", h.MinRTT)
	}
	if h.MaxRTT != 20*time.Millisecond {
		t.Errorf("MaxRTT: want 20ms, got %v", h.MaxRTT)
	}
	wantAvg := 15 * time.Millisecond
	if h.AvgRTT != wantAvg {
		t.Errorf("AvgRTT: want %v, got %v", wantAvg, h.AvgRTT)
	}
	if h.LastRTT != 20*time.Millisecond {
		t.Errorf("LastRTT: want 20ms, got %v", h.LastRTT)
	}
}

func TestMTRStats_StarHop(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(2)

	// hop at TTL=2 never responds
	m.RecordLoss(2)
	m.RecordLoss(2)

	hops := m.View()
	h := hops[1] // TTL=2
	if h.IP != "" {
		t.Errorf("IP: want empty for * hop, got %q", h.IP)
	}
	if h.LossPct != 100.0 {
		t.Errorf("LossPct: want 100, got %v", h.LossPct)
	}
	if h.Sent != 2 {
		t.Errorf("Sent: want 2, got %d", h.Sent)
	}
	if h.Recv != 0 {
		t.Errorf("Recv: want 0, got %d", h.Recv)
	}
}

func TestMTRStats_EnsureLenGrowsAndPreserves(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(2)
	m.RecordReply(1, "10.0.0.1", "AS1234", "", "", 5*time.Millisecond)

	// Grow: existing hop data must be preserved
	m.EnsureLen(4)
	hops := m.View()
	if len(hops) != 4 {
		t.Fatalf("want 4 hops after grow, got %d", len(hops))
	}
	if hops[0].IP != "10.0.0.1" {
		t.Errorf("hop 0 IP lost after grow: got %q", hops[0].IP)
	}
	if hops[0].ASN != "AS1234" {
		t.Errorf("hop 0 ASN lost after grow: got %q", hops[0].ASN)
	}
}

func TestMTRStats_EnsureLenShrinks(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(5)
	m.RecordReply(5, "10.0.0.5", "", "", "", 50*time.Millisecond)

	// Shrink: EnsureLen with smaller n is a no-op (don't discard data)
	m.EnsureLen(3)
	hops := m.View()
	if len(hops) != 5 {
		t.Errorf("EnsureLen shrink should be no-op; want 5, got %d", len(hops))
	}
}

func TestMTRStats_SetIP(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(1)
	m.SetIP(1, "192.168.1.1", "AS64512", "", "")

	hops := m.View()
	if hops[0].IP != "192.168.1.1" {
		t.Errorf("IP: want 192.168.1.1, got %q", hops[0].IP)
	}
	if hops[0].ASN != "AS64512" {
		t.Errorf("ASN: want AS64512, got %q", hops[0].ASN)
	}
}

func TestMTRStats_CountryOrgStored(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(2)

	m.RecordReply(1, "8.8.8.8", "AS15169", "US", "Google LLC", 10*time.Millisecond)
	m.SetIP(2, "1.1.1.1", "AS13335", "AU", "Cloudflare")

	hops := m.View()

	if hops[0].Country != "US" {
		t.Errorf("hop1 Country: want US, got %q", hops[0].Country)
	}
	if hops[0].Org != "Google LLC" {
		t.Errorf("hop1 Org: want Google LLC, got %q", hops[0].Org)
	}
	if hops[1].Country != "AU" {
		t.Errorf("hop2 Country: want AU, got %q", hops[1].Country)
	}
	if hops[1].Org != "Cloudflare" {
		t.Errorf("hop2 Org: want Cloudflare, got %q", hops[1].Org)
	}
}

func TestMTRStats_Reset(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(2)
	m.RecordReply(1, "10.0.0.1", "", "", "", 5*time.Millisecond)
	m.Reset()

	hops := m.View()
	if len(hops) != 0 {
		t.Errorf("after Reset, want 0 hops, got %d", len(hops))
	}
}

func TestMTRStats_ConcurrentAccess(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(5)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ttl := (n % 5) + 1
			if n%3 == 0 {
				m.RecordLoss(ttl)
			} else {
				m.RecordReply(ttl, "10.0.0.1", "", "", "", time.Duration(n)*time.Millisecond)
			}
		}(i)
	}
	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.View()
		}()
	}
	wg.Wait()
}

func TestTargetStats_MTRIntegration(t *testing.T) {
	ts := NewTargetStats("example.com")
	m := ts.MTR()
	if m == nil {
		t.Fatal("MTR() returned nil")
	}

	// same instance on second call
	if ts.MTR() != m {
		t.Error("MTR() must return the same instance")
	}

	m.EnsureLen(2)
	m.RecordReply(1, "10.0.0.1", "", "", "", 10*time.Millisecond)

	// GetView should include MTR snapshot
	v := ts.GetView()
	if len(v.MTRHops) != 2 {
		t.Fatalf("GetView MTRHops: want 2, got %d", len(v.MTRHops))
	}
	if v.MTRHops[0].IP != "10.0.0.1" {
		t.Errorf("GetView MTRHops[0].IP: want 10.0.0.1, got %q", v.MTRHops[0].IP)
	}
}

func TestMTRStats_RouteSnapshot(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(3)
	m.SetIP(1, "10.0.0.1", "", "", "")
	m.SetIP(2, "10.0.0.2", "", "", "")
	m.SetIP(3, "8.8.8.8", "", "", "")

	snap := m.RouteSnapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3 IPs, got %d", len(snap))
	}
	want := []string{"10.0.0.1", "10.0.0.2", "8.8.8.8"}
	for i, ip := range want {
		if snap[i] != ip {
			t.Errorf("snap[%d]: want %q, got %q", i, ip, snap[i])
		}
	}
}

func TestMTRStats_CheckFlap_NoChange(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(2)
	m.SetIP(1, "10.0.0.1", "", "", "")
	m.SetIP(2, "8.8.8.8", "", "", "")

	prev := m.RouteSnapshot()
	changed, _ := m.CheckFlap(prev, time.Now())
	if changed {
		t.Error("identical routes should not trigger a flap")
	}
	count, _, _ := m.FlapInfo()
	if count != 0 {
		t.Errorf("flapCount: want 0, got %d", count)
	}
}

func TestMTRStats_CheckFlap_IPChanged(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(3)
	m.SetIP(1, "10.0.0.1", "", "", "")
	m.SetIP(2, "10.0.0.2", "", "", "")
	m.SetIP(3, "8.8.8.8", "", "", "")

	prev := m.RouteSnapshot()
	// Simulate re-discovery: hop 2 changes
	m.SetIP(2, "10.0.0.99", "", "", "")

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	changed, desc := m.CheckFlap(prev, now)
	if !changed {
		t.Fatal("expected flap to be detected")
	}
	if !strings.Contains(desc, "10.0.0.2") || !strings.Contains(desc, "10.0.0.99") {
		t.Errorf("desc should mention old and new IPs, got %q", desc)
	}

	count, at, storedDesc := m.FlapInfo()
	if count != 1 {
		t.Errorf("flapCount: want 1, got %d", count)
	}
	if !at.Equal(now) {
		t.Errorf("lastFlapAt: want %v, got %v", now, at)
	}
	if storedDesc != desc {
		t.Errorf("storedDesc mismatch: want %q, got %q", desc, storedDesc)
	}
}

func TestMTRStats_CheckFlap_LengthChanged(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(2)
	m.SetIP(1, "10.0.0.1", "", "", "")
	m.SetIP(2, "8.8.8.8", "", "", "")

	prev := m.RouteSnapshot()
	// Simulate re-discovery: path grew by one hop
	m.EnsureLen(3)
	m.SetIP(3, "1.2.3.4", "", "", "")

	changed, _ := m.CheckFlap(prev, time.Now())
	if !changed {
		t.Error("path length change should trigger a flap")
	}
}

func TestMTRStats_CheckFlap_Cumulative(t *testing.T) {
	m := NewMTRStats()
	m.EnsureLen(1)
	m.SetIP(1, "10.0.0.1", "", "", "")

	for i := 2; i <= 4; i++ {
		prev := m.RouteSnapshot()
		m.SetIP(1, fmt.Sprintf("10.0.0.%d", i), "", "", "")
		changed, _ := m.CheckFlap(prev, time.Now())
		if !changed {
			t.Errorf("iteration %d: expected flap", i)
		}
	}
	count, _, _ := m.FlapInfo()
	if count != 3 {
		t.Errorf("flapCount: want 3, got %d", count)
	}
}

func TestTargetStats_GetView_FlapFields(t *testing.T) {
	ts := NewTargetStats("example.com")
	m := ts.MTR()
	m.EnsureLen(2)
	m.SetIP(1, "10.0.0.1", "", "", "")
	m.SetIP(2, "8.8.8.8", "", "", "")

	prev := m.RouteSnapshot()
	m.SetIP(1, "10.0.0.99", "", "", "")
	now := time.Now()
	m.CheckFlap(prev, now)

	v := ts.GetView()
	if v.MTRFlapCount != 1 {
		t.Errorf("MTRFlapCount: want 1, got %d", v.MTRFlapCount)
	}
	if v.MTRLastFlapAt.IsZero() {
		t.Error("MTRLastFlapAt should not be zero after a flap")
	}
}

func TestTargetStats_ResetClearsMTR(t *testing.T) {
	ts := NewTargetStats("example.com")
	m := ts.MTR()
	m.EnsureLen(3)
	m.RecordReply(1, "10.0.0.1", "", "", "", 5*time.Millisecond)

	ts.Reset()
	hops := m.View()
	if len(hops) != 0 {
		t.Errorf("Reset should clear MTR hops, got %d", len(hops))
	}
}
