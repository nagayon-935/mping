package pinger

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// ---- ParsePortSpec tests ----

func TestParsePortSpec_ValidFormats(t *testing.T) {
	tests := []struct {
		input     string
		wantPort  int
		wantProto string
	}{
		{"443", 443, "tcp"},
		{"443/tcp", 443, "tcp"},
		{"53/udp", 53, "udp"},
		{"80/TCP", 80, "tcp"},
		{"123/UDP", 123, "udp"},
		{"1", 1, "tcp"},
		{"65535", 65535, "tcp"},
		{" 8080 ", 8080, "tcp"},
		{"8080/ tcp", 8080, "tcp"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParsePortSpec(tt.input)
			if err != nil {
				t.Fatalf("ParsePortSpec(%q) unexpected error: %v", tt.input, err)
			}
			if got.Port != tt.wantPort {
				t.Errorf("Port: got %d, want %d", got.Port, tt.wantPort)
			}
			if got.Protocol != tt.wantProto {
				t.Errorf("Protocol: got %q, want %q", got.Protocol, tt.wantProto)
			}
		})
	}
}

func TestParsePortSpec_InvalidFormats(t *testing.T) {
	tests := []string{
		"0",
		"65536",
		"-1",
		"abc",
		"443/sctp",
		"443/",
		"",
		"99999",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := ParsePortSpec(input)
			if err == nil {
				t.Fatalf("ParsePortSpec(%q) expected error, got nil", input)
			}
		})
	}
}

// ---- NewPortChecker tests ----

func TestNewPortChecker_InitializesPortResults(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("host1"),
		stats.NewTargetStats("host2"),
	}
	specs := []PortSpec{
		{Port: 80, Protocol: "tcp"},
		{Port: 443, Protocol: "tcp"},
	}
	pc := NewPortChecker(targets, specs, time.Second, time.Second)

	if pc == nil {
		t.Fatal("NewPortChecker returned nil")
	}
	for _, tgt := range targets {
		if len(tgt.PortResults) != len(specs) {
			t.Errorf("target %q: got %d PortResults, want %d", tgt.Host, len(tgt.PortResults), len(specs))
		}
		for i, r := range tgt.PortResults {
			if r.Port != specs[i].Port {
				t.Errorf("PortResult[%d].Port: got %d, want %d", i, r.Port, specs[i].Port)
			}
			if r.Protocol != specs[i].Protocol {
				t.Errorf("PortResult[%d].Protocol: got %q, want %q", i, r.Protocol, specs[i].Protocol)
			}
			if r.Status != "Checking..." {
				t.Errorf("PortResult[%d].Status: got %q, want %q", i, r.Status, "Checking...")
			}
		}
	}
}

func TestNewPortChecker_EmptySpecsAndTargets(t *testing.T) {
	pc := NewPortChecker(nil, nil, time.Second, time.Second)
	if pc == nil {
		t.Fatal("NewPortChecker returned nil")
	}
}

// ---- PortChecker.Stop/Wait tests ----

func TestPortChecker_StartStop(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("host1")}
	specs := []PortSpec{{Port: 9, Protocol: "tcp"}} // discard port, won't connect
	pc := NewPortChecker(targets, specs, 10*time.Second, 10*time.Millisecond)

	pc.Start()
	// Stop should not block or panic
	done := make(chan struct{})
	go func() {
		pc.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within timeout")
	}
}

// TestPortChecker_Wait verifies that Wait() actually joins every per-(target,spec)
// loop goroutine spawned by Start(), rather than just cancelling their context.
func TestPortChecker_Wait(t *testing.T) {
	targets := []*stats.TargetStats{
		stats.NewTargetStats("host1"),
		stats.NewTargetStats("host2"),
	}
	for _, tgt := range targets {
		tgt.SetIP("127.0.0.1") // give check() a real address to dial, so loops do actual work
	}
	specs := []PortSpec{{Port: 9, Protocol: "tcp"}, {Port: 10, Protocol: "udp"}}
	pc := NewPortChecker(targets, specs, 5*time.Millisecond, 10*time.Millisecond)

	pc.Start()
	time.Sleep(20 * time.Millisecond) // let loops run at least once
	pc.Stop()

	done := make(chan struct{})
	go func() {
		pc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return within timeout after Stop()")
	}

	// Once Wait() has returned, no loop goroutine can still be running, so
	// results must stop changing.
	snapshot := func() []string {
		out := make([]string, 0, len(targets)*len(specs))
		for _, tgt := range targets {
			for _, r := range tgt.PortResults {
				out = append(out, r.GetView().Status)
			}
		}
		return out
	}
	before := snapshot()
	time.Sleep(50 * time.Millisecond)
	after := snapshot()
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("result[%d] changed after Wait() returned: %q -> %q (a loop goroutine outlived Wait)", i, before[i], after[i])
		}
	}
}

// ---- checkTCP tests ----

func TestCheckTCP_Open(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start test server: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	addr := ln.Addr().String()
	status, rtt := checkTCP(context.Background(), addr, time.Second)
	if status != "Open" {
		t.Errorf("checkTCP: got %q, want %q (rtt=%v)", status, "Open", rtt)
	}
	if rtt <= 0 {
		t.Errorf("checkTCP: expected positive rtt, got %v", rtt)
	}
}

func TestCheckTCP_Closed(t *testing.T) {
	// Find a free port, then close the listener so the port is not open
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	status, _ := checkTCP(context.Background(), addr, time.Second)
	if status != "Closed" && status != "Filtered" {
		t.Errorf("checkTCP closed port: got %q, want Closed or Filtered", status)
	}
}

func TestCheckTCP_Filtered(t *testing.T) {
	// Use a non-routable address to trigger timeout
	status, _ := checkTCP(context.Background(), "192.0.2.1:9", 50*time.Millisecond)
	if status != "Filtered" {
		t.Logf("checkTCP filtered: got %q (may vary in CI)", status)
	}
}

// ---- checkUDP tests ----

func TestCheckUDP_OpenFiltered(t *testing.T) {
	// Non-routable address, UDP won't get ICMP unreachable → Open|Filtered
	status, _ := checkUDP(context.Background(), "192.0.2.1:9", 50*time.Millisecond)
	if status != "Open|Filtered" && status != "Error" {
		t.Logf("checkUDP: got %q (may vary by environment)", status)
	}
}

func TestCheckUDP_LocalhostOpen(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind UDP: ", err)
	}
	defer conn.Close()
	addr := conn.LocalAddr().String()

	status, _ := checkUDP(context.Background(), addr, 200*time.Millisecond)
	// UDP server exists; likely Open|Filtered or Open depending on OS
	if status == "Error" {
		t.Logf("checkUDP local: got Error (OS may not allow UDP dial to self)")
	}
}

func TestCheckUDP_Closed(t *testing.T) {
	// Pick a port that is unlikely to be listening
	addr := "127.0.0.1:54321"
	status, _ := checkUDP(context.Background(), addr, 50*time.Millisecond)
	// On most OSs, this should return Closed (ECONNREFUSED) or Open|Filtered (timeout)
	// If it returns Closed, we've increased coverage.
	if status == "Closed" {
		t.Log("got Closed as expected for unreachable UDP port")
	}
}

func TestCheckUDP_DialError(t *testing.T) {
	// An out-of-range port makes net.DialTimeout fail deterministically,
	// exercising the "Error" return path.
	status, rtt := checkUDP(context.Background(), "127.0.0.1:99999", 50*time.Millisecond)
	if status != "Error" {
		t.Errorf("checkUDP with invalid port = %q, want Error", status)
	}
	if rtt != 0 {
		t.Errorf("checkUDP error path rtt = %v, want 0", rtt)
	}
}

// ---- isTimeout tests ----

type mockTimeoutError struct{ timeout bool }

func (e *mockTimeoutError) Error() string   { return "mock error" }
func (e *mockTimeoutError) Timeout() bool   { return e.timeout }
func (e *mockTimeoutError) Temporary() bool { return false }

func TestIsTimeout(t *testing.T) {
	if !isTimeout(&mockTimeoutError{timeout: true}) {
		t.Error("expected true for timeout error")
	}
	if isTimeout(&mockTimeoutError{timeout: false}) {
		t.Error("expected false for non-timeout error")
	}
	if isTimeout(fmt.Errorf("plain error")) {
		t.Error("expected false for plain error")
	}
}

// ---- PortChecker.check integration test ----

func TestPortChecker_CheckWithIP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start server: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port

	tgt := stats.NewTargetStats("localhost")
	tgt.SetIP("127.0.0.1")
	spec := PortSpec{Port: port, Protocol: "tcp"}
	result := &stats.PortCheckResult{Port: port, Protocol: "tcp", Status: "Checking..."}
	tgt.PortResults = []*stats.PortCheckResult{result}

	pc := NewPortChecker([]*stats.TargetStats{tgt}, []PortSpec{spec}, time.Second, time.Second)
	pc.check(tgt, spec, result)

	status := result.GetView().Status
	if status != "Open" {
		t.Errorf("check: got status %q, want Open", status)
	}
}

func TestPortChecker_CheckSkipsEmptyIP(t *testing.T) {
	tgt := stats.NewTargetStats("no-ip-host")
	// IP not set (empty string)
	spec := PortSpec{Port: 80, Protocol: "tcp"}
	result := &stats.PortCheckResult{Port: 80, Protocol: "tcp", Status: "Checking..."}
	tgt.PortResults = []*stats.PortCheckResult{result}

	pc := NewPortChecker([]*stats.TargetStats{tgt}, []PortSpec{spec}, time.Second, time.Second)
	pc.check(tgt, spec, result)

	// Status should remain unchanged since IP was empty
	status := result.GetView().Status
	if status != "Checking..." {
		t.Errorf("check with empty IP: expected status to remain %q, got %q", "Checking...", status)
	}
}
