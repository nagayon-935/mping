package pinger

import (
	"context"
	"fmt"
	"net"
	"reflect"
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
	pc := NewPortChecker(targets, specs, time.Second, time.Second, BindConfig{})

	if pc == nil {
		t.Fatal("NewPortChecker returned nil")
	}
	for _, tgt := range targets {
		results := tgt.GetView().PortResults
		if len(results) != len(specs) {
			t.Errorf("target %q: got %d PortResults, want %d", tgt.Host(), len(results), len(specs))
		}
		for i, r := range results {
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
	pc := NewPortChecker(nil, nil, time.Second, time.Second, BindConfig{})
	if pc == nil {
		t.Fatal("NewPortChecker returned nil")
	}
}

// ---- PortChecker.Stop/Wait tests ----

func TestPortChecker_StartStop(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("host1")}
	specs := []PortSpec{{Port: 9, Protocol: "tcp"}} // discard port, won't connect
	pc := NewPortChecker(targets, specs, 10*time.Second, 10*time.Millisecond, BindConfig{})

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
	pc := NewPortChecker(targets, specs, 5*time.Millisecond, 10*time.Millisecond, BindConfig{})

	pc.Start()
	for _, tgt := range targets { // let every loop complete a check
		tgt := tgt
		for i := range specs {
			eventually(t, 5*time.Second, "first port check", func() bool {
				return tgt.GetView().PortResults[i].Status != "Checking..."
			})
		}
	}
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
			for _, r := range tgt.GetView().PortResults {
				out = append(out, r.Status)
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
	status, rtt := checkTCP(context.Background(), NewBoundDialer("tcp", time.Second, BindConfig{}), addr)
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

	status, _ := checkTCP(context.Background(), NewBoundDialer("tcp", time.Second, BindConfig{}), addr)
	if status != "Closed" && status != "Filtered" {
		t.Errorf("checkTCP closed port: got %q, want Closed or Filtered", status)
	}
}

func TestCheckTCP_Filtered(t *testing.T) {
	// An expired or cancelled context is reported as Filtered (no answer
	// before the deadline), without depending on any network behaviour.
	expired, cancelExpired := context.WithTimeout(context.Background(), 0)
	defer cancelExpired()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, ctx := range map[string]context.Context{"deadline exceeded": expired, "cancelled": cancelled} {
		if status, _ := checkTCP(ctx, NewBoundDialer("tcp", time.Second, BindConfig{}), "127.0.0.1:9"); status != "Filtered" {
			t.Errorf("%s: checkTCP = %q, want Filtered", name, status)
		}
	}
}

// ---- checkUDP tests ----

// udpListener binds a loopback UDP socket for a checkUDP scenario.
func udpListener(t *testing.T) net.PacketConn {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind UDP: ", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestCheckUDP_Outcomes(t *testing.T) {
	t.Run("a reply means Open", func(t *testing.T) {
		srv := udpListener(t)
		go func() {
			buf := make([]byte, 16)
			if _, from, err := srv.ReadFrom(buf); err == nil {
				srv.WriteTo([]byte{1}, from)
			}
		}()
		status, rtt := checkUDP(context.Background(), NewBoundDialer("udp", time.Second, BindConfig{}), srv.LocalAddr().String(), time.Second)
		if status != "Open" || rtt <= 0 {
			t.Fatalf("checkUDP = (%q, %v), want Open with a positive rtt", status, rtt)
		}
	})

	t.Run("silence until the timeout means Open|Filtered", func(t *testing.T) {
		srv := udpListener(t) // bound but never answers
		status, rtt := checkUDP(context.Background(), NewBoundDialer("udp", 50*time.Millisecond, BindConfig{}), srv.LocalAddr().String(), 50*time.Millisecond)
		if status != "Open|Filtered" || rtt < 40*time.Millisecond {
			t.Fatalf("checkUDP = (%q, %v), want Open|Filtered after ~50ms", status, rtt)
		}
	})

	t.Run("ICMP port unreachable means Closed", func(t *testing.T) {
		srv := udpListener(t)
		addr := srv.LocalAddr().String()
		srv.Close() // nothing listens any more: loopback answers with port unreachable
		status, _ := checkUDP(context.Background(), NewBoundDialer("udp", time.Second, BindConfig{}), addr, time.Second)
		if status != "Closed" {
			t.Fatalf("checkUDP = %q, want Closed", status)
		}
	})

	t.Run("cancellation interrupts a pending read", func(t *testing.T) {
		srv := udpListener(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			// The probe datagram proves checkUDP is past its setup and about
			// to block in Read; cancel then.
			buf := make([]byte, 16)
			if _, _, err := srv.ReadFrom(buf); err == nil {
				cancel()
			}
		}()
		done := make(chan struct{})
		go func() {
			checkUDP(ctx, NewBoundDialer("udp", time.Minute, BindConfig{}), srv.LocalAddr().String(), time.Minute)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("checkUDP stayed blocked in Read after its context was cancelled")
		}
	})
}

func TestCheckUDP_DialError(t *testing.T) {
	// An out-of-range port makes net.DialTimeout fail deterministically,
	// exercising the "Error" return path.
	status, rtt := checkUDP(context.Background(), NewBoundDialer("udp", 50*time.Millisecond, BindConfig{}), "127.0.0.1:99999", 50*time.Millisecond)
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
	result := stats.NewPortCheckResult(port, "tcp")

	pc := NewPortChecker([]*stats.TargetStats{tgt}, []PortSpec{spec}, time.Second, time.Second, BindConfig{})
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
	result := stats.NewPortCheckResult(80, "tcp")

	pc := NewPortChecker([]*stats.TargetStats{tgt}, []PortSpec{spec}, time.Second, time.Second, BindConfig{})
	pc.check(tgt, spec, result)

	// Status should remain unchanged since IP was empty
	status := result.GetView().Status
	if status != "Checking..." {
		t.Errorf("check with empty IP: expected status to remain %q, got %q", "Checking...", status)
	}
}

// ---- DNS wait behaviour tests ----

// TestPortChecker_LoopWaitsForIP verifies that the first port check is deferred
// until the target IP is resolved, instead of being silently skipped.
func TestPortChecker_LoopWaitsForIP(t *testing.T) {
	tgt := stats.NewTargetStats("delayed-ip")
	spec := PortSpec{Port: 9, Protocol: "tcp"}
	pc := NewPortChecker([]*stats.TargetStats{tgt}, []PortSpec{spec}, time.Hour, 10*time.Millisecond, BindConfig{})

	pc.Start()
	defer pc.Stop()

	if status := tgt.GetView().PortResults[0].Status; status != "Checking..." {
		t.Fatalf("expected Checking... immediately, got %q", status)
	}

	// Without an IP the first check is deferred, not run and not skipped.
	time.Sleep(20 * time.Millisecond)
	if status := tgt.GetView().PortResults[0].Status; status != "Checking..." {
		t.Fatalf("check ran before the IP was resolved: %q", status)
	}
	tgt.SetIP("127.0.0.1")

	eventually(t, 2*time.Second, "status leaves Checking... once the IP is set", func() bool {
		return tgt.GetView().PortResults[0].Status != "Checking..."
	})
}

// TestPortChecker_LoopStopsWhileWaitingForIP verifies that Stop() cancels the
// DNS-wait loop before maxDNSWait expires.
func TestPortChecker_LoopStopsWhileWaitingForIP(t *testing.T) {
	tgt := stats.NewTargetStats("never-resolved")
	spec := PortSpec{Port: 9, Protocol: "tcp"}
	pc := NewPortChecker([]*stats.TargetStats{tgt}, []PortSpec{spec}, time.Hour, 10*time.Millisecond, BindConfig{})

	pc.Start()
	pc.Stop()

	done := make(chan struct{})
	go func() {
		pc.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return after Stop() while waiting for IP")
	}
}

func TestPortTargetEditsKeepSurvivingResult(t *testing.T) {
	a, b := stats.NewTargetStats("a"), stats.NewTargetStats("b")
	a.SetIP("127.0.0.1")
	b.SetIP("127.0.0.1")
	pc := NewPortChecker([]*stats.TargetStats{a}, []PortSpec{{Port: 1, Protocol: "tcp"}}, time.Hour, 10*time.Millisecond, BindConfig{})
	pc.Start()
	defer func() { pc.Stop(); pc.Wait() }()
	awaitTarget(t, a, func(v stats.TargetView) bool { return v.PortResults[0].ClosedCount > 0 })
	before := a.GetView().PortResults[0]
	pc.AddTarget(b)
	awaitTarget(t, b, func(v stats.TargetView) bool { return v.PortResults[0].ClosedCount > 0 })
	pc.RemoveTarget(b)
	// The interval is an hour, so a's result cannot change on its own; a
	// replaced result would differ at least in LastChange.
	if after := a.GetView().PortResults[0]; !reflect.DeepEqual(after, before) {
		t.Fatalf("surviving port statistics were reset: %+v -> %+v", before, after)
	}
}

func TestParsePortSpecRejectsTrailingCharacters(t *testing.T) {
	for _, raw := range []string{"443garbage/tcp", "443.5", "443:80", "80 90/tcp", "53x/udp"} {
		if spec, err := ParsePortSpec(raw); err == nil {
			t.Errorf("accepted %q as %+v", raw, spec)
		}
	}
}

func TestPortChecker_AddTargetTwiceKeepsOneRun(t *testing.T) {
	tgt := stats.NewTargetStats("a")
	tgt.SetIP("127.0.0.1")
	pc := NewPortChecker(nil, []PortSpec{{Port: 1, Protocol: "tcp"}}, time.Hour, 50*time.Millisecond, BindConfig{})
	defer func() { pc.Stop(); pc.Wait() }()

	pc.AddTarget(tgt)
	first := pc.targetRuns[tgt]
	pc.AddTarget(tgt)

	if len(pc.targetRuns) != 1 || pc.targetRuns[tgt] != first {
		t.Fatalf("a second AddTarget replaced the running checks: %d runs", len(pc.targetRuns))
	}
}

// TestPortChecker_StopDuringCheckKeepsLastResult: shutting down while a check
// is in flight must not turn the interrupted check into a recorded result.
func TestPortChecker_StopDuringCheckKeepsLastResult(t *testing.T) {
	srv := udpListener(t) // receives the probe but never answers
	port := srv.LocalAddr().(*net.UDPAddr).Port
	tgt := stats.NewTargetStats("127.0.0.1")
	tgt.SetIP("127.0.0.1")
	spec := PortSpec{Port: port, Protocol: "udp"}
	pc := NewPortChecker([]*stats.TargetStats{tgt}, []PortSpec{spec}, time.Hour, time.Minute, BindConfig{})
	r := pc.results[0][0]
	before := r.GetView()

	go func() {
		buf := make([]byte, 16)
		if _, _, err := srv.ReadFrom(buf); err == nil {
			pc.Stop() // the probe is on the wire: interrupt the pending read
		}
	}()
	done := make(chan struct{})
	go func() { pc.check(tgt, spec, r); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("check stayed blocked after Stop")
	}

	if got := r.GetView(); !reflect.DeepEqual(got, before) {
		t.Fatalf("interrupted check changed the result: %+v -> %+v", before, got)
	}
}
