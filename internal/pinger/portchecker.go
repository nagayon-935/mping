package pinger

import (
	"context"
	"errors"
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
	targets []*stats.TargetStats
	specs   []PortSpec
	// results[i][j] is the PortCheckResult for targets[i] × specs[j].
	// Stored here so Start() never reads t.PortResults directly, eliminating
	// the data race between NewPortChecker and concurrent GetView() calls.
	results  [][]*stats.PortCheckResult
	interval time.Duration
	timeout  time.Duration
	bind     BindConfig
	// Per-protocol dialers built once from bind: net.Dialer.LocalAddr must be
	// a *net.TCPAddr for tcp and a *net.UDPAddr for udp, so the two cannot
	// share one dialer.
	tcpDialer *net.Dialer
	udpDialer *net.Dialer
	ctx       context.Context
	cancel    context.CancelFunc
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// NewPortChecker creates a PortChecker and initialises PortResults on each
// target. bind carries the -S source address and -I interface name so port
// checks leave the host by the same path as the ICMP probes; a zero BindConfig
// leaves dialling exactly as it was before those flags were wired in.
func NewPortChecker(targets []*stats.TargetStats, specs []PortSpec, interval, timeout time.Duration, bind BindConfig) *PortChecker {
	results := make([][]*stats.PortCheckResult, len(targets))
	for i, t := range targets {
		results[i] = make([]*stats.PortCheckResult, len(specs))
		for j, s := range specs {
			results[i][j] = &stats.PortCheckResult{Port: s.Port, Protocol: s.Protocol, Status: "Checking..."}
		}
		t.SetPortResults(results[i])
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &PortChecker{
		targets:   targets,
		specs:     specs,
		results:   results,
		interval:  interval,
		timeout:   timeout,
		bind:      bind,
		tcpDialer: newBoundDialer("tcp", timeout, bind),
		udpDialer: newBoundDialer("udp", timeout, bind),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// BindConfig returns the source/interface binding the checks are dialling
// with.
func (pc *PortChecker) BindConfig() BindConfig { return pc.bind }

// Start launches one goroutine per (target, spec) pair.
func (pc *PortChecker) Start() {
	for i, t := range pc.targets {
		for j, spec := range pc.specs {
			result := pc.results[i][j] // use internally stored pointer; never reads t.PortResults
			pc.wg.Add(1)
			go pc.loop(t, spec, result)
		}
	}
}

// Stop signals all goroutines to exit. Safe to call multiple times.
func (pc *PortChecker) Stop() {
	pc.stopOnce.Do(func() { pc.cancel() })
}

// Wait blocks until all check goroutines have exited. Call Stop first.
func (pc *PortChecker) Wait() {
	pc.wg.Wait()
}

// maxDNSWait bounds how long the first port check waits for the pinger to
// resolve the target IP. Without this the initial check is silently skipped
// when IP is still empty, leaving the status stuck at "Checking...".
const maxDNSWait = 5 * time.Second

func (pc *PortChecker) loop(t *stats.TargetStats, spec PortSpec, result *stats.PortCheckResult) {
	defer pc.wg.Done()

	// Defer the first check until the target IP is resolved (or we time out),
	// so the user sees a real Open/Closed/Filtered result on the first tick.
	if t.GetView().IP == "" {
		waitTicker := time.NewTicker(100 * time.Millisecond)
		defer waitTicker.Stop()
		deadline := time.Now().Add(maxDNSWait)
		for t.GetView().IP == "" && time.Now().Before(deadline) {
			select {
			case <-pc.ctx.Done():
				return
			case <-waitTicker.C:
			}
		}
	}

	pc.check(t, spec, result)
	ticker := time.NewTicker(pc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-pc.ctx.Done():
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
		status, rtt = checkTCP(pc.ctx, pc.tcpDialer, addr)
	case "udp":
		status, rtt = checkUDP(pc.ctx, pc.udpDialer, addr, pc.timeout)
	}
	result.SetResult(status, rtt)
}

func checkTCP(ctx context.Context, dialer *net.Dialer, addr string) (string, time.Duration) {
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	rtt := time.Since(start)
	if err != nil {
		if isTimeout(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "Filtered", rtt
		}
		return "Closed", rtt
	}
	conn.Close()
	return "Open", rtt
}

func checkUDP(ctx context.Context, dialer *net.Dialer, addr string, timeout time.Duration) (string, time.Duration) {
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return "Error", 0
	}
	defer conn.Close()

	doneChan := make(chan struct{})
	defer close(doneChan)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-doneChan:
		}
	}()

	start := time.Now()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "Error", 0
	}
	if _, err := conn.Write([]byte{}); err != nil {
		return "Error", 0
	}

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
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
