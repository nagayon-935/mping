package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// requireAll returns a frame predicate that holds once every marker is on screen.
func requireAll(markers ...string) func(frame string) bool {
	return func(frame string) bool {
		for _, m := range markers {
			if !strings.Contains(frame, m) {
				return false
			}
		}
		return true
	}
}

// TestRunRendersMonitorPanes drives the full TUI with each combination of
// the traceroute and port monitors and checks that their tables reach the
// screen with the target's data.
func TestRunRendersMonitorPanes(t *testing.T) {
	ported := func(host, ip string, port int, status string) *stats.TargetStats {
		target := stats.NewTargetStats(host)
		target.SetIP(ip)
		result := &stats.PortCheckResult{Port: port, Protocol: "tcp"}
		result.SetResult(status, 5*time.Millisecond)
		target.SetPortResults([]*stats.PortCheckResult{result})
		return target
	}
	tests := []struct {
		name  string
		opts  func() RunOptions
		wants []string
	}{
		{
			name: "traceroute",
			opts: func() RunOptions {
				target := stats.NewTargetStats("example.com")
				target.SetTraceHops([]string{"10.0.0.1", "10.0.0.2", "1.1.1.1"})
				return RunOptions{Targets: []*stats.TargetStats{target}, SourceIPv4: "10.0.0.1", TraceEnabled: true}
			},
			wants: []string{"Traceroute Monitor", "10.0.0.1 -> 10.0.0.2 -> 1.1.1.1"},
		},
		{
			name: "port",
			opts: func() RunOptions {
				target := stats.NewTargetStats("example.com")
				target.SetIP("127.0.0.1")
				target.SetPortResults([]*stats.PortCheckResult{
					{Port: 443, Protocol: "tcp", Status: "Open"},
					{Port: 80, Protocol: "tcp", Status: "Closed"},
				})
				return RunOptions{Targets: []*stats.TargetStats{target}, SourceIPv4: "10.0.0.1", PortEnabled: true}
			},
			wants: []string{"Port Monitor", "443/tcp", "HTTPS", "Open", "80/tcp", "Closed"},
		},
		{
			name: "port before any result arrives",
			opts: func() RunOptions {
				target := stats.NewTargetStats("example.com")
				target.SetIP("127.0.0.1")
				return RunOptions{Targets: []*stats.TargetStats{target}, PortEnabled: true}
			},
			wants: []string{"Port Monitor", "Waiting for results"},
		},
		{
			name: "port that changed status keeps its last change time",
			opts: func() RunOptions {
				target := stats.NewTargetStats("example.com")
				target.SetIP("127.0.0.1")
				result := &stats.PortCheckResult{Port: 443, Protocol: "tcp", Status: "Checking..."}
				target.SetPortResults([]*stats.PortCheckResult{result})
				result.SetResult("Open", 10*time.Millisecond)
				result.SetResult("Closed", 5*time.Millisecond) // the status change sets LastChange
				return RunOptions{Targets: []*stats.TargetStats{target}, PortEnabled: true}
			},
			wants: []string{"Port Monitor", "Closed", "ago"},
		},
		{
			name: "traceroute and port for two targets",
			opts: func() RunOptions {
				first := ported("example.com", "127.0.0.1", 443, "Open")
				first.SetTraceHops([]string{"10.0.0.1", "1.1.1.1"})
				second := ported("example2.com", "8.8.8.8", 80, "Filtered")
				second.SetTraceHops([]string{"10.0.0.9"})
				return RunOptions{
					Targets:    []*stats.TargetStats{first, second},
					SourceIPv4: "10.0.0.1", SourceIPv6: "2001::1",
					TraceEnabled: true, PortEnabled: true,
				}
			},
			wants: []string{
				"Traceroute Monitor", "10.0.0.1 -> 1.1.1.1", "10.0.0.9",
				"Port Monitor", "443/tcp", "Open", "80/tcp", "Filtered",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := startTUI(t, tt.opts(), 200, 50)
			h.waitFrame(strings.Join(tt.wants, ", "), requireAll(tt.wants...))
			h.quit()
		})
	}
}

// TestRunShowsHostAddressOnce checks that a target whose host is already an
// IP address is not rendered as "ip (ip)".
func TestRunShowsHostAddressOnce(t *testing.T) {
	target := stats.NewTargetStats("1.2.3.4")
	target.SetIP("1.2.3.4")
	h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{target}}, 200, 50)
	frame := h.wait("1.2.3.4")
	if strings.Contains(frame, "1.2.3.4 (1.2.3.4)") {
		t.Errorf("host shown twice in the Dst IP column:\n%s", frame)
	}
	h.quit()
}

// TestRunLogsPingProblems checks that loss and threshold alerts raised from
// target statistics are written to the Log pane.
func TestRunLogsPingProblems(t *testing.T) {
	tests := []struct {
		name  string
		setup func(target *stats.TargetStats)
		wants []string
	}{
		{
			name: "a lost packet logs its error",
			setup: func(target *stats.TargetStats) {
				target.IncSent()
				target.OnFailure("Timeout")
			},
			wants: []string{"example.com (10.0.0.1): Timeout"},
		},
		{
			name: "RTT and jitter above the critical thresholds",
			setup: func(target *stats.TargetStats) {
				// Alternating 10ms/300ms: the last RTT is above the 200ms
				// critical threshold and the smoothed jitter climbs past 50ms.
				for i := range 10 {
					rtt := 10 * time.Millisecond
					if i%2 == 1 {
						rtt = 300 * time.Millisecond
					}
					target.IncSent()
					target.OnSuccess(rtt, 64)
				}
			},
			wants: []string{"RTT 300ms", "Jitter "},
		},
		{
			name: "loss ratio above the critical threshold",
			setup: func(target *stats.TargetStats) {
				for range 9 {
					target.IncSent()
					target.OnFailure("Timeout")
				}
				target.IncSent()
				target.OnSuccess(10*time.Millisecond, 64)
			},
			wants: []string{"Loss Ratio 90.0%"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			target.SetIP("1.1.1.1")
			tt.setup(target)
			h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{target}, SourceIPv4: "10.0.0.1"}, 200, 50)
			h.waitFrame(strings.Join(tt.wants, ", "), requireAll(tt.wants...))
			h.quit()
		})
	}
}

func TestRunShowsInitialLogs(t *testing.T) {
	h := startTUI(t, RunOptions{
		Targets:     []*stats.TargetStats{stats.NewTargetStats("example.com")},
		InitialLogs: []string{"[red]initial error 1[-]", "[red]initial error 2[-]"},
	}, 200, 50)
	h.waitFrame("both initial logs", requireAll("initial error 1", "initial error 2"))
	h.quit()
}

// TestRunPortStatusChangeLogged flips a port from Open to Closed after the
// first render and expects the transition in the Log pane.
func TestRunPortStatusChangeLogged(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.SetIP("127.0.0.1")
	result := &stats.PortCheckResult{Port: 443, Protocol: "tcp"}
	result.SetResult("Open", 5*time.Millisecond)
	target.SetPortResults([]*stats.PortCheckResult{result})
	h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{target}, PortEnabled: true}, 200, 50)
	// The first render records the initial status; only then is a change visible.
	h.wait("Open")
	result.SetResult("Closed", 0)
	h.wait("443/tcp: Open → Closed")
	h.quit()
}

// TestRunDoneChShowsFinished checks that a closed DoneCh (count limit
// reached) switches the footer to the finished hint.
func TestRunDoneChShowsFinished(t *testing.T) {
	doneCh := make(chan struct{})
	h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{stats.NewTargetStats("example.com")}, DoneCh: doneCh}, 200, 50)
	h.wait("Ping Monitor")
	close(doneCh)
	h.wait("Finished. Press 'q' to quit")
	h.quit()
}
