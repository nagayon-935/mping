package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/rivo/tview"
)

func TestPortServiceName(t *testing.T) {
	tests := []struct {
		port     int
		protocol string
		want     string
	}{
		{443, "tcp", "HTTPS"},
		{80, "tcp", "HTTP"},
		{53, "tcp", "DNS"},
		{53, "udp", "DNS"},
		{22, "tcp", "SSH"},
		{67, "udp", "DHCP"},
		{123, "udp", "NTP"},
		{3306, "tcp", "MySQL"},
		{5432, "tcp", "PostgreSQL"},
		{6379, "tcp", "Redis"},
		{27017, "tcp", "MongoDB"},
		// Protocol mismatch → Unknown
		{80, "udp", "Unknown"},
		{443, "udp", "Unknown"},
		// Unknown port → Unknown
		{9999, "tcp", "Unknown"},
		{0, "tcp", "Unknown"},
	}
	for _, tt := range tests {
		got := portServiceName(tt.port, tt.protocol)
		if got != tt.want {
			t.Errorf("portServiceName(%d, %q): got %q, want %q", tt.port, tt.protocol, got, tt.want)
		}
	}
}

func TestRenderTracerouteTable(t *testing.T) {
	t.Run("no data targets", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		got := renderTracerouteTable([]*stats.TargetStats{target}, 80)
		if !strings.Contains(got, "Host") || !strings.Contains(got, "Route") {
			t.Fatalf("expected table headers, got: %s", got)
		}
	})

	t.Run("with data targets full mode", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		setRouteHops(target, "1.1.1.1", "2.2.2.2", "8.8.8.8")
		got := renderTracerouteTable([]*stats.TargetStats{target}, 120)
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname in output, got: %s", got)
		}
		if !strings.Contains(got, "Hops") {
			t.Fatalf("expected full mode with Hops column, got: %s", got)
		}
	})

	t.Run("compact mode narrow width", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		setRouteHops(target, "1.1.1.1")
		got := renderTracerouteTable([]*stats.TargetStats{target}, 30)
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname in output, got: %s", got)
		}
		if strings.Contains(got, "Hops") {
			t.Fatalf("expected compact mode without Hops column, got: %s", got)
		}
	})

	t.Run("discovery failure", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.MTR().SetError("resolve example.com: no such host")
		got := renderTracerouteTable([]*stats.TargetStats{target}, 120)
		if !strings.Contains(got, "Route discovery failed: resolve example.com: no such host") {
			t.Fatalf("expected the discovery error, got: %s", got)
		}
	})

	t.Run("hop names are escaped", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		setRouteHops(target, "10.0.0.1")
		target.MTR().SetPTR(1, "10.0.0.1", "gw[red]")
		got := renderTracerouteTable([]*stats.TargetStats{target}, 120)
		if !strings.Contains(got, "10.0.0.1 gw[red[]") {
			t.Fatalf("expected an escaped PTR name, got: %s", got)
		}
	})

	t.Run("route changes are flagged", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		setRouteHops(target, "10.0.0.1")
		target.MTR().CheckFlap([]string{"10.0.0.9"}, time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local))
		got := renderTracerouteTable([]*stats.TargetStats{target}, 120)
		if !strings.Contains(got, "[FLAP 1 times 03:04:05]") {
			t.Fatalf("expected a flap badge, got: %s", got)
		}
	})

	t.Run("multiple targets", func(t *testing.T) {
		t1 := stats.NewTargetStats("a.com")
		setRouteHops(t1, "1.1.1.1")
		t2 := stats.NewTargetStats("b.com")
		setRouteHops(t2, "2.2.2.2")
		got := renderTracerouteTable([]*stats.TargetStats{t1, t2}, 120)
		if !strings.Contains(got, "a.com") || !strings.Contains(got, "b.com") {
			t.Fatalf("expected both hosts in output, got: %s", got)
		}
	})
}

func TestRenderPortMonitorTable(t *testing.T) {
	t.Run("no data targets", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		lastStatuses := make(map[string]string)
		errorLogs := []string{}
		errorView := tview.NewTextView()
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, func(msg string) { appendErrorLog(&errorLogs, errorView, msg) })
		if !strings.Contains(got, "Waiting for results") {
			t.Fatalf("expected waiting message, got: %s", got)
		}
	})

	t.Run("with data full mode", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetIP("1.1.1.1")
		result := stats.NewPortCheckResult(443, "tcp")
		result.SetResult("Open", 5*time.Millisecond)
		target.SetPortResults([]*stats.PortCheckResult{result})

		lastStatuses := make(map[string]string)
		errorLogs := []string{}
		errorView := tview.NewTextView()
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, func(msg string) { appendErrorLog(&errorLogs, errorView, msg) })
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname, got: %s", got)
		}
		if !strings.Contains(got, "Service") {
			t.Fatalf("expected full mode with Service column, got: %s", got)
		}
	})

	t.Run("compact mode narrow width", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetIP("1.1.1.1")
		result := stats.NewPortCheckResult(80, "tcp")
		result.SetResult("Closed", 0)
		target.SetPortResults([]*stats.PortCheckResult{result})

		lastStatuses := make(map[string]string)
		errorLogs := []string{}
		errorView := tview.NewTextView()
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 50, lastStatuses, func(msg string) { appendErrorLog(&errorLogs, errorView, msg) })
		if !strings.Contains(got, "example.com") {
			t.Fatalf("expected hostname, got: %s", got)
		}
		if strings.Contains(got, "Service") {
			t.Fatalf("expected compact mode without Service column, got: %s", got)
		}
	})

	t.Run("status change logs", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		target.SetIP("1.1.1.1")
		result := stats.NewPortCheckResult(443, "tcp")
		result.SetResult("Open", 5*time.Millisecond)
		target.SetPortResults([]*stats.PortCheckResult{result})

		lastStatuses := map[string]string{
			targetViewKey(target.GetView()) + "|443/tcp": "Closed",
		}
		errorLogs := []string{}
		errorView := tview.NewTextView()
		renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, func(msg string) { appendErrorLog(&errorLogs, errorView, msg) })

		if len(errorLogs) == 0 {
			t.Fatal("expected status change log entry")
		}
		if !strings.Contains(errorLogs[0], "Open") {
			t.Fatalf("expected log to mention Open, got: %s", errorLogs[0])
		}
	})
}

func TestPortMonitorColumnWidths(t *testing.T) {
	target := stats.NewTargetStats("edge-router-with-a-long-name.example.com")
	port := stats.NewPortCheckResult(443, "tcp")
	port.SetResult("Open", 5*time.Millisecond)
	target.SetPortResults([]*stats.PortCheckResult{port})

	for _, tt := range []struct {
		name  string
		avail int
		want  []int
	}{
		{"full gives spare width to Target", 140, []int{49, 9, 9, 15, 6, 5, 5, 5, 13, 13}},
		{"compact shrinks Target", 55, []int{20, 9, 15, 6}},
		{"too narrow for full: long host keeps its room", 100, []int{65, 9, 15, 6}},
		{"narrower than compact: Target shrinks, no overflow", 40, []int{8, 9, 15, 6}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			table := renderPortMonitorTable([]*stats.TargetStats{target}, tt.avail, map[string]string{}, nil)
			top, _, _ := strings.Cut(table, "\n")
			var widths []int
			for col := range strings.SplitSeq(strings.TrimSuffix(strings.TrimPrefix(top, "[white]┌"), "┐[-]"), "┬") {
				widths = append(widths, tview.TaggedStringWidth(col))
			}
			if !slices.Equal(widths, tt.want) {
				t.Errorf("column widths = %v, want %v", widths, tt.want)
			}
		})
	}
}

func TestPortMonitorLatency(t *testing.T) {
	for _, tt := range []struct {
		status string
		rtt    time.Duration
		last   string
	}{
		{"Open", 5 * time.Millisecond, "5ms"},
		{"Filtered", time.Second, "timeout"},
		{"Open|Filtered", time.Second, "timeout"},
		{"Closed", time.Millisecond, "–"},
		{"Error", 0, "–"},
		{"Checking...", 0, "–"},
	} {
		t.Run(tt.status, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			port := stats.NewPortCheckResult(443, "tcp")
			port.SetResult("Open", 3*time.Millisecond)
			port.SetResult(tt.status, tt.rtt)
			target.SetPortResults([]*stats.PortCheckResult{port})
			table := renderPortMonitorTable([]*stats.TargetStats{target}, 140, map[string]string{}, nil)
			for line := range strings.SplitSeq(table, "\n") {
				if !strings.Contains(line, "example.com") {
					continue
				}
				cells := strings.Split(line, "│")
				if got := strings.TrimSpace(tview.NewTextView().SetDynamicColors(true).SetText(cells[5]).GetText(true)); got != tt.last {
					t.Errorf("Last = %q, want %q in %s", got, tt.last, line)
				}
				if tt.last == "timeout" && !strings.HasPrefix(cells[5], "[gray]") {
					t.Errorf("timeout is missing muted colour: %s", cells[5])
				}
				if tt.status != "Open" {
					for _, cell := range cells[6:9] {
						if strings.TrimSpace(tview.NewTextView().SetDynamicColors(true).SetText(cell).GetText(true)) != "–" {
							t.Errorf("historical latency cell = %q, want dash", cell)
						}
					}
				}
				return
			}
			t.Fatalf("missing port row:\n%s", table)
		})
	}
}
