package ui

import (
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
		if !strings.Contains(got, "[FLAP ×1 03:04:05]") {
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
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, &errorLogs, errorView)
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
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, &errorLogs, errorView)
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
		got := renderPortMonitorTable([]*stats.TargetStats{target}, 30, lastStatuses, &errorLogs, errorView)
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
		renderPortMonitorTable([]*stats.TargetStats{target}, 120, lastStatuses, &errorLogs, errorView)

		if len(errorLogs) == 0 {
			t.Fatal("expected status change log entry")
		}
		if !strings.Contains(errorLogs[0], "Open") {
			t.Fatalf("expected log to mention Open, got: %s", errorLogs[0])
		}
	})
}
