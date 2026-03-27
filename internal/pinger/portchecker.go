package pinger

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// PortSpec represents a single port/protocol combination to check.
type PortSpec struct {
	Port     int
	Protocol string // "tcp" or "udp"
}

// String returns the port spec in "port/proto" format.
func (s PortSpec) String() string {
	return fmt.Sprintf("%d/%s", s.Port, s.Protocol)
}

// ParsePortSpec parses a port specification string.
// Accepted formats: "443", "443/tcp", "53/udp" (defaults to tcp).
func ParsePortSpec(s string) (PortSpec, error) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, "/", 2)
	port := 0
	if _, err := fmt.Sscanf(parts[0], "%d", &port); err != nil || port < 1 || port > 65535 {
		return PortSpec{}, fmt.Errorf("invalid port: %q", parts[0])
	}
	proto := "tcp"
	if len(parts) == 2 {
		proto = strings.ToLower(strings.TrimSpace(parts[1]))
		if proto != "tcp" && proto != "udp" {
			return PortSpec{}, fmt.Errorf("invalid protocol %q: must be tcp or udp", parts[1])
		}
	}
	return PortSpec{Port: port, Protocol: proto}, nil
}

// PortChecker runs TCP/UDP port checks for a set of targets.
type PortChecker struct {
	targets  []*stats.TargetStats
	specs    []PortSpec
	interval time.Duration
	timeout  time.Duration
	done     chan struct{}
	stopOnce sync.Once
}

// NewPortChecker creates a PortChecker and initialises PortResults on each target.
func NewPortChecker(targets []*stats.TargetStats, specs []PortSpec, interval, timeout time.Duration) *PortChecker {
	for _, t := range targets {
		results := make([]*stats.PortCheckResult, len(specs))
		for i, s := range specs {
			results[i] = &stats.PortCheckResult{Port: s.Port, Protocol: s.Protocol, Status: "Checking..."}
		}
		t.PortResults = results
	}
	return &PortChecker{
		targets:  targets,
		specs:    specs,
		interval: interval,
		timeout:  timeout,
		done:     make(chan struct{}),
	}
}

// Start launches one goroutine per (target, spec) pair.
func (pc *PortChecker) Start() {
	for _, t := range pc.targets {
		for i, spec := range pc.specs {
			result := t.PortResults[i]
			go pc.loop(t, spec, result)
		}
	}
}

// Stop signals all goroutines to exit. Safe to call multiple times.
func (pc *PortChecker) Stop() {
	pc.stopOnce.Do(func() { close(pc.done) })
}

func (pc *PortChecker) loop(t *stats.TargetStats, spec PortSpec, result *stats.PortCheckResult) {
	pc.check(t, spec, result)
	ticker := time.NewTicker(pc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-pc.done:
			return
		case <-ticker.C:
			pc.check(t, spec, result)
		}
	}
}

func (pc *PortChecker) check(t *stats.TargetStats, spec PortSpec, result *stats.PortCheckResult) {
	ip := t.GetView().IP
	if ip == "" {
		return
	}
	addr := net.JoinHostPort(ip, fmt.Sprintf("%d", spec.Port))

	var status string
	var rtt time.Duration
	switch spec.Protocol {
	case "tcp":
		status, rtt = checkTCP(addr, pc.timeout)
	case "udp":
		status, rtt = checkUDP(addr, pc.timeout)
	}
	result.SetResult(status, rtt)
}

func checkTCP(addr string, timeout time.Duration) (string, time.Duration) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	rtt := time.Since(start)
	if err != nil {
		if isTimeout(err) {
			return "Filtered", rtt
		}
		return "Closed", rtt
	}
	conn.Close()
	return "Open", rtt
}

func checkUDP(addr string, timeout time.Duration) (string, time.Duration) {
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return "Error", 0
	}
	defer conn.Close()

	start := time.Now()
	conn.SetDeadline(time.Now().Add(timeout))
	conn.Write([]byte{})

	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	rtt := time.Since(start)
	if err != nil {
		if isTimeout(err) {
			// No ICMP port unreachable received — port is open or filtered
			return "Open|Filtered", rtt
		}
		// ICMP port unreachable received — port is closed
		return "Closed", rtt
	}
	return "Open", rtt
}

func isTimeout(err error) bool {
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout()
	}
	return false
}
