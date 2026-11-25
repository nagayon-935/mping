package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// buildMTRTarget creates a TargetStats with a set of pre-loaded hops for testing.
func buildMTRTarget(host, ip string, hops []stats.HopView) *stats.TargetStats {
	t := stats.NewTargetStats(host)
	t.SetIP(ip)
	m := t.MTR()
	m.EnsureLen(len(hops))
	for _, h := range hops {
		if h.Recv > 0 {
			for i := 0; i < h.Recv; i++ {
				m.RecordReply(h.TTL, h.IP, h.ASN, h.Country, h.Org, h.AvgRTT)
			}
		}
		for i := 0; i < h.Sent-h.Recv; i++ {
			m.RecordLoss(h.TTL)
		}
		if h.IP != "" {
			m.SetIP(h.TTL, h.IP, h.ASN, h.Country, h.Org)
		}
	}
	return t
}

func TestMtrIPStr(t *testing.T) {
	tests := []struct {
		name string
		hop  stats.HopView
		want string
	}{
		{"star hop", stats.HopView{IP: ""}, "*"},
		{"no ASN", stats.HopView{IP: "10.0.0.1"}, "10.0.0.1"},
		{
			"ASN with operator name",
			stats.HopView{IP: "8.8.8.8", ASN: "AS15169", Country: "US", Org: "Google LLC"},
			"8.8.8.8 (AS15169 Google LLC)",
		},
		{
			"ASN without operator name falls back to AS number only",
			stats.HopView{IP: "8.8.4.4", ASN: "AS15169", Country: "US"},
			"8.8.4.4 (AS15169)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mtrIPStr(tt.hop); got != tt.want {
				t.Errorf("mtrIPStr(%+v) = %q, want %q", tt.hop, got, tt.want)
			}
		})
	}
}

func TestRenderMTRTable_BasicStructure(t *testing.T) {
	target := buildMTRTarget("8.8.8.8", "8.8.8.8", []stats.HopView{
		{TTL: 1, IP: "192.168.1.1", Sent: 10, Recv: 10, AvgRTT: 2 * time.Millisecond},
		{TTL: 2, IP: "", Sent: 10, Recv: 0}, // star hop
		{TTL: 3, IP: "8.8.8.8", Sent: 10, Recv: 10, AvgRTT: 12 * time.Millisecond},
	})

	out := renderMTRTable([]*stats.TargetStats{target}, 120, sourceIPs{v4: "192.168.1.1"})

	// Must have box-drawing borders
	if !strings.Contains(out, "┌") {
		t.Error("missing top-left corner ┌")
	}
	if !strings.Contains(out, "└") {
		t.Error("missing bottom-left corner └")
	}
	if !strings.Contains(out, "│") {
		t.Error("missing column separator │")
	}

	// Hop numbers must appear
	if !strings.Contains(out, "1.") {
		t.Error("missing hop 1")
	}
	if !strings.Contains(out, "2.") {
		t.Error("missing hop 2")
	}
	if !strings.Contains(out, "3.") {
		t.Error("missing hop 3")
	}

	// Star hop
	if !strings.Contains(out, "*") {
		t.Error("star hop (*) not rendered")
	}

	// IPs
	if !strings.Contains(out, "192.168.1.1") {
		t.Error("hop 1 IP not in output")
	}
	if !strings.Contains(out, "8.8.8.8") {
		t.Error("hop 3 IP not in output")
	}

	// Column headers
	for _, col := range []string{"Hop", "Host", "Loss%", "Snt", "Last", "Avg"} {
		if !strings.Contains(out, col) {
			t.Errorf("column header %q missing", col)
		}
	}
}

func TestRenderMTRTable_NoHops_ShowsDiscovering(t *testing.T) {
	target := stats.NewTargetStats("1.1.1.1")
	target.SetIP("1.1.1.1")

	out := renderMTRTable([]*stats.TargetStats{target}, 120, sourceIPs{v4: "192.168.1.1"})
	if !strings.Contains(out, "Discovering") {
		t.Error("expected 'Discovering...' when no hops available")
	}
}

func TestRenderMTRTable_CompactVsFull(t *testing.T) {
	target := buildMTRTarget("8.8.8.8", "8.8.8.8", []stats.HopView{
		{TTL: 1, IP: "10.0.0.1", Sent: 5, Recv: 5, AvgRTT: 1 * time.Millisecond},
	})

	fullOut := renderMTRTable([]*stats.TargetStats{target}, 200, sourceIPs{v4: "10.0.0.1"})
	compactOut := renderMTRTable([]*stats.TargetStats{target}, 80, sourceIPs{v4: "10.0.0.1"})

	// Full mode must have Min/Max/Jitter columns
	if !strings.Contains(fullOut, "Min") {
		t.Error("full mode missing Min column")
	}
	if !strings.Contains(fullOut, "Jitter") {
		t.Error("full mode missing Jitter column")
	}
	if !strings.Contains(fullOut, "Recv") {
		t.Error("full mode missing Recv column")
	}

	// Compact mode must not have Min/Max/Jitter columns
	if strings.Contains(compactOut, "Jitter") {
		t.Error("compact mode should not show Jitter column")
	}
}

func TestRenderMTRTable_MultipleTargets(t *testing.T) {
	t1 := buildMTRTarget("8.8.8.8", "8.8.8.8", []stats.HopView{
		{TTL: 1, IP: "10.0.0.1", Sent: 5, Recv: 5, AvgRTT: 1 * time.Millisecond},
	})
	t2 := buildMTRTarget("1.1.1.1", "1.1.1.1", []stats.HopView{
		{TTL: 1, IP: "10.0.0.1", Sent: 5, Recv: 5, AvgRTT: 1 * time.Millisecond},
	})

	out := renderMTRTable([]*stats.TargetStats{t1, t2}, 120, sourceIPs{v4: "10.0.0.1"})

	// Both target labels must appear
	if !strings.Contains(out, "8.8.8.8") {
		t.Error("target 1 label missing")
	}
	if !strings.Contains(out, "1.1.1.1") {
		t.Error("target 2 label missing")
	}
}

func TestRenderMTRTable_HopNumberNeverTruncated(t *testing.T) {
	// Hop numbers up to 30 must not show "..."
	var hops []stats.HopView
	for i := 1; i <= 30; i++ {
		hops = append(hops, stats.HopView{TTL: i, IP: "10.0.0.1", Sent: 1, Recv: 1})
	}
	target := buildMTRTarget("dest", "1.2.3.4", hops)

	out := renderMTRTable([]*stats.TargetStats{target}, 160, sourceIPs{v4: "10.0.0.1"})

	if strings.Contains(out, "...") {
		// Find the offending line
		for line := range strings.SplitSeq(out, "\n") {
			if strings.Contains(line, "...") {
				t.Errorf("truncation found in line: %q", line)
			}
		}
	}
}

func TestRenderMTRTable_EffectiveLoss(t *testing.T) {
	tests := []struct {
		name     string
		destRecv int
		want     []string
		absent   []string
	}{
		{"intermediate loss muted", 10, []string{"○ OK", "[gray] *", "[gray] 100.0%"}, []string{"loss from hop", "× 100.0%"}},
		{"low loss persists", 9, []string{"[gray] 100.0%", " 10.0%"}, []string{"loss from hop", "[gray] 10.0%", "△ 10.0%"}},
		{"warning loss persists", 5, []string{"○ OK · loss from hop 2", "[orange] △ 100.0%", "[orange] △ 50.0%"}, nil},
		{"destination silent", 0, []string{"○ OK · loss from hop 2", "[red] × 100.0%"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := buildMTRTarget("8.8.8.8", "8.8.8.8", []stats.HopView{
				{TTL: 1, IP: "10.0.0.1", Sent: 10, Recv: 10, AvgRTT: time.Millisecond},
				{TTL: 2, Sent: 10},
				{TTL: 3, IP: "8.8.8.8", Sent: 10, Recv: tt.destRecv, AvgRTT: 10 * time.Millisecond},
			})
			target.IncSent()
			target.OnSuccess(10*time.Millisecond, 64)
			got := renderMTRTable([]*stats.TargetStats{target}, 75, sourceIPs{v4: "10.0.0.1"})
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("MTR table missing %q in:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("MTR table unexpectedly contains %q in:\n%s", absent, got)
				}
			}
		})
	}
}
