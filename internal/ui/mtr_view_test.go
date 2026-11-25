package ui

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"

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

func TestRouteViewsShowDiscoveryErrorWithOrWithoutHops(t *testing.T) {
	const hopIP = "192.0.2.1"
	const failure = "destination unreachable"
	for _, tc := range []struct {
		name string
		hops []stats.HopView
	}{
		{name: "empty"},
		{name: "partial", hops: []stats.HopView{{TTL: 1, IP: hopIP}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := buildMTRTarget("example.com", "192.0.2.10", tc.hops)
			target.MTR().SetError(failure)
			targets := []*stats.TargetStats{target}
			for _, view := range []struct {
				name string
				text string
			}{
				{"traceroute table", renderTracerouteTable(targets, 120)},
				{"MTR table", renderMTRTable(targets, 120, sourceIPs{})},
				{"traceroute details", renderHostDetails(target, RunOptions{Route: RoutePath})},
				{"MTR details", renderHostDetails(target, RunOptions{Route: RouteMonitor})},
			} {
				t.Run(view.name, func(t *testing.T) {
					if !strings.Contains(view.text, "Route discovery failed: "+failure) {
						t.Fatalf("missing route error in %q", view.text)
					}
					if len(tc.hops) > 0 && !strings.Contains(view.text, hopIP) {
						t.Fatalf("missing discovered hop in %q", view.text)
					}
					for _, pending := range []string{"Discovering...", "Route not discovered yet."} {
						if strings.Contains(view.text, pending) {
							t.Fatalf("route error appeared alongside pending status %q", pending)
						}
					}
				})
			}
		})
	}
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
	if !strings.Contains(out, "[gray] Discovering...") {
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
	lossCell := regexp.MustCompile(`│(\[[a-z]+\]) +([^│\[]+%) +(?:\[-\])?\[white\]│`)
	tests := []struct {
		name          string
		destRecv      int
		destIP        string
		wantLoss      []string
		wantMutedHost bool
	}{
		{"intermediate loss muted", 10, "8.8.8.8", []string{"[white]0.0%", "[gray]100.0%", "[white]0.0%"}, true},
		{"low loss persists", 9, "8.8.8.8", []string{"[white]0.0%", "[gray]100.0%", "[white]10.0%"}, true},
		{"warning loss persists", 5, "8.8.8.8", []string{"[white]0.0%", "[orange]△ 100.0%", "[orange]△ 50.0%"}, false},
		{"destination missing", 0, "", []string{"[white]0.0%", "[red]× 100.0%", "[red]× 100.0%"}, false},
		{"known destination silent", 0, "8.8.8.8", []string{"[white]0.0%", "[red]× 100.0%", "[red]× 100.0%"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := buildMTRTarget("8.8.8.8", "8.8.8.8", []stats.HopView{
				{TTL: 1, IP: "10.0.0.1", Sent: 10, Recv: 10, AvgRTT: time.Millisecond},
				{TTL: 2, Sent: 10},
				{TTL: 3, IP: tt.destIP, Sent: 10, Recv: tt.destRecv, AvgRTT: 10 * time.Millisecond},
			})
			target.IncSent()
			target.OnSuccess(time.Second, 64)
			got := renderMTRTable([]*stats.TargetStats{target}, 75, sourceIPs{v4: "10.0.0.1"})
			plain := tview.NewTextView().SetDynamicColors(true).SetText(got).GetText(true)
			header := strings.Trim(strings.SplitN(plain, "\n", 3)[1], "│ ")
			if header != "10.0.0.1 -> 8.8.8.8" {
				t.Errorf("MTR target header = %q, want only the route label", header)
			}
			var losses []string
			for _, match := range lossCell.FindAllStringSubmatch(got, -1) {
				losses = append(losses, match[1]+match[2])
			}
			if !slices.Equal(losses, tt.wantLoss) {
				t.Errorf("MTR loss cells = %q, want %q:\n%s", losses, tt.wantLoss, got)
			}
			if muted := strings.Contains(got, "│[gray] * "); muted != tt.wantMutedHost {
				t.Errorf("muted host marker = %v, want %v:\n%s", muted, tt.wantMutedHost, got)
			}
		})
	}
}

func TestRenderMTRTable_LongLatenciesAndHostWidth(t *testing.T) {
	const ip = "fe80::1"
	const latency = 99999990 * time.Microsecond
	target := buildMTRTarget("router.example", "FE80::1%en0", []stats.HopView{
		{TTL: 1, IP: ip, Sent: 2, Recv: 2, AvgRTT: latency},
	})
	target.MTR().SetPTR(1, ip, "router.example")

	for _, tt := range []struct {
		name      string
		width     int
		wantCells int
		wantHost  string
	}{
		{"full retains hop identity and latency", 220, 4, ip + " router.example"},
		{"narrow ellipsizes host and retains latency", 85, 2, ip + " router.e..."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := renderMTRTable([]*stats.TargetStats{target}, tt.width, sourceIPs{})
			assertBoxTableWidth(t, out, tt.width)
			if !strings.Contains(out, tt.wantHost) {
				t.Errorf("missing hop identity %q:\n%s", tt.wantHost, out)
			}
			plain := tview.NewTextView().SetDynamicColors(true).SetText(out).GetText(true)
			var paddedLatencies int
			for cell := range strings.SplitSeq(plain, "│") {
				if strings.HasPrefix(cell, " ") && strings.TrimLeft(cell, " ") == "99999.99ms " {
					paddedLatencies++
				}
			}
			if paddedLatencies != tt.wantCells {
				t.Errorf("found %d right-aligned latency cells with padding, want %d:\n%s", paddedLatencies, tt.wantCells, plain)
			}
		})
	}
}

func TestRenderMTRTable_NumericWidthsStayPutAsHopNameChanges(t *testing.T) {
	const ip = "192.0.2.1"
	target := buildMTRTarget("example.com", "192.0.2.2", []stats.HopView{
		{TTL: 1, IP: ip, Sent: 2, Recv: 2, AvgRTT: 5 * time.Millisecond},
	})
	for _, width := range []int{85, 220} {
		render := func(ptr string) (string, string) {
			target.MTR().SetPTR(1, ip, ptr)
			out := renderMTRTable([]*stats.TargetStats{target}, width, sourceIPs{})
			assertBoxTableWidth(t, out, width)
			return strings.Split(out, "\n")[2], out
		}
		before, _ := render("r1")
		after, out := render(strings.Repeat("long-label.", 20) + "example.com")
		if before != after {
			t.Errorf("width %d: column borders moved when the hop name changed:\n%s\n%s", width, before, after)
		}
		if !strings.Contains(out, ip+" long-l") || !strings.Contains(out, "... ") {
			t.Errorf("width %d: expected an ellipsized hop name:\n%s", width, out)
		}
	}
}
