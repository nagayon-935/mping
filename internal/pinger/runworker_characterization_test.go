package pinger

// Characterization tests for runWorker, written *before* splitting it into a
// send-ticker / expiry-sweep select loop (see the "async probe loop"
// refactor). runWorker today is stop-and-wait: it blocks in waitForReply for
// up to `timeout` after every send, so a target that never replies is only
// re-probed once per `timeout`, not once per `interval`. These tests pin the
// *observable* behavior that must survive the rewrite unchanged: stats
// transitions (Sent/Recv/Loss/LastRTT/LastTTL/LastError), the CSV log
// format/status strings/seq numbering, Count's send-then-drain semantics,
// prompt termination on Stop()/Close(), and silent discarding of a reply
// that doesn't match any outstanding probe. The one behavior explicitly
// allowed to change is send cadence no longer being throttled by timeout.
//
// After the refactor, every test in this file must still pass unmodified.

import (
	"bytes"
	"errors"
	"net"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// csvFields splits p.log's CSV output into records of
// [timestamp, host, ip, seq, status, rttMs, ttl, errMsg].
func csvFields(t *testing.T, logOutput string) [][]string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(logOutput, "\n"), "\n")
	var records [][]string
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, ",", 8)
		if len(fields) != 8 {
			t.Fatalf("malformed CSV line %q: got %d fields, want 8", line, len(fields))
		}
		records = append(records, fields)
	}
	return records
}

// TestCharacterization_RunWorker_SuccessSentRecvCSV pins the transition of
// Sent/Recv/LastRTT/LastTTL on a successful reply, and the exact CSV log
// line runWorker emits for it: seq numbering starts at 1, status is "OK",
// RTT is a positive ms value, and the error column is empty.
func TestCharacterization_RunWorker_SuccessSentRecvCSV(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	var logBuf bytes.Buffer
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	ch := make(chan Reply, 1)
	p.targetChans[id] = ch

	go func() {
		time.Sleep(5 * time.Millisecond)
		ch <- Reply{TTL: 55, Seq: 1}
	}()

	p.runWorker(target, id, 10*time.Millisecond, 200*time.Millisecond)

	view := target.GetView()
	if view.Sent != 1 {
		t.Fatalf("Sent = %d, want 1", view.Sent)
	}
	if view.Recv != 1 {
		t.Fatalf("Recv = %d, want 1", view.Recv)
	}
	if view.LastTTL != 55 {
		t.Fatalf("LastTTL = %d, want 55", view.LastTTL)
	}
	if view.LastRTT <= 0 {
		t.Fatalf("LastRTT = %v, want > 0", view.LastRTT)
	}
	if view.Loss != 0 {
		t.Fatalf("Loss = %d, want 0", view.Loss)
	}

	records := csvFields(t, logBuf.String())
	if len(records) != 1 {
		t.Fatalf("log records = %d, want 1: %q", len(records), logBuf.String())
	}
	rec := records[0]
	if rec[3] != "1" {
		t.Fatalf("seq column = %q, want %q", rec[3], "1")
	}
	if rec[4] != "OK" {
		t.Fatalf("status column = %q, want %q", rec[4], "OK")
	}
	if rec[6] != "55" {
		t.Fatalf("ttl column = %q, want %q", rec[6], "55")
	}
	if rec[7] != "" {
		t.Fatalf("error column = %q, want empty", rec[7])
	}
}

// TestCharacterization_RunWorker_TimeoutCSV pins Loss/LastError on a timeout
// and the resulting "Timeout" CSV line.
func TestCharacterization_RunWorker_TimeoutCSV(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	var logBuf bytes.Buffer
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	p.runWorker(target, id, 5*time.Millisecond, 20*time.Millisecond)

	view := target.GetView()
	if view.Loss != 1 {
		t.Fatalf("Loss = %d, want 1", view.Loss)
	}
	if view.Recv != 0 {
		t.Fatalf("Recv = %d, want 0", view.Recv)
	}
	if view.LastError != "Timeout" {
		t.Fatalf("LastError = %q, want %q", view.LastError, "Timeout")
	}

	records := csvFields(t, logBuf.String())
	if len(records) != 1 {
		t.Fatalf("log records = %d, want 1: %q", len(records), logBuf.String())
	}
	rec := records[0]
	if rec[4] != "Timeout" {
		t.Fatalf("status column = %q, want %q", rec[4], "Timeout")
	}
	if rec[7] != "Request timed out" {
		t.Fatalf("error column = %q, want %q", rec[7], "Request timed out")
	}
}

// TestCharacterization_RunWorker_ICMPErrorCSV pins Loss/LastError and the
// "ICMPError" CSV line produced when the receiver delivers an ICMP error
// reply (Reply.Err != "").
func TestCharacterization_RunWorker_ICMPErrorCSV(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	var logBuf bytes.Buffer
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	ch := make(chan Reply, 1)
	p.targetChans[id] = ch

	go func() {
		time.Sleep(5 * time.Millisecond)
		ch <- Reply{Seq: 1, Err: "Time Exceeded"}
	}()

	p.runWorker(target, id, 10*time.Millisecond, 200*time.Millisecond)

	view := target.GetView()
	if view.Loss != 1 {
		t.Fatalf("Loss = %d, want 1", view.Loss)
	}
	if view.LastError != "Time Exceeded" {
		t.Fatalf("LastError = %q, want %q", view.LastError, "Time Exceeded")
	}

	records := csvFields(t, logBuf.String())
	if len(records) != 1 {
		t.Fatalf("log records = %d, want 1: %q", len(records), logBuf.String())
	}
	rec := records[0]
	if rec[4] != "ICMPError" {
		t.Fatalf("status column = %q, want %q", rec[4], "ICMPError")
	}
	if rec[7] != "Time Exceeded" {
		t.Fatalf("error column = %q, want %q", rec[7], "Time Exceeded")
	}
}

// TestCharacterization_RunWorker_SendErrorCSV pins the "SendError" CSV line
// and that a failed write does NOT increment Sent (IncSent only fires after
// a successful WriteTo).
func TestCharacterization_RunWorker_SendErrorCSV(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakeErrPacketConn{err: errors.New("send failed")}
	p.Count = 1

	var logBuf bytes.Buffer
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	done := make(chan struct{})
	go func() {
		p.runWorker(target, id, 5*time.Millisecond, 200*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runWorker did not stop after a send error with Count=1")
	}

	view := target.GetView()
	if view.Sent != 0 {
		t.Fatalf("Sent = %d, want 0 (IncSent must not fire on a failed write)", view.Sent)
	}
	if view.Loss != 1 {
		t.Fatalf("Loss = %d, want 1", view.Loss)
	}

	records := csvFields(t, logBuf.String())
	if len(records) != 1 {
		t.Fatalf("log records = %d, want 1: %q", len(records), logBuf.String())
	}
	rec := records[0]
	if rec[4] != "SendError" {
		t.Fatalf("status column = %q, want %q", rec[4], "SendError")
	}
	if rec[7] != "send failed" {
		t.Fatalf("error column = %q, want %q", rec[7], "send failed")
	}
}

// TestCharacterization_RunWorker_MismatchedReplyDiscarded pins the rule that
// a reply whose Seq doesn't match any outstanding probe is silently
// discarded: it must not be counted as Recv, Loss, or logged, and the
// eventual correctly-matching reply must still be recorded normally.
func TestCharacterization_RunWorker_MismatchedReplyDiscarded(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 1

	var logBuf bytes.Buffer
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	ch := make(chan Reply, 2)
	p.targetChans[id] = ch

	go func() {
		time.Sleep(5 * time.Millisecond)
		ch <- Reply{TTL: 1, Seq: 99} // stale/unrelated seq: must be discarded
		ch <- Reply{TTL: 64, Seq: 1} // the real match
	}()

	p.runWorker(target, id, 10*time.Millisecond, 200*time.Millisecond)

	view := target.GetView()
	if view.Recv != 1 {
		t.Fatalf("Recv = %d, want 1 (mismatched reply must not be counted)", view.Recv)
	}
	if view.Loss != 0 {
		t.Fatalf("Loss = %d, want 0", view.Loss)
	}
	if view.LastTTL != 64 {
		t.Fatalf("LastTTL = %d, want 64 (from the matching reply, not the discarded one)", view.LastTTL)
	}

	records := csvFields(t, logBuf.String())
	if len(records) != 1 {
		t.Fatalf("log records = %d, want 1 (discarded reply must not be logged): %q", len(records), logBuf.String())
	}
}

// TestCharacterization_RunWorker_StopNoGoroutineLeak pins that Stop()/Close()
// terminates an unlimited (Count=0) worker promptly, with no leaked
// goroutines (ticker/timer goroutines included).
func TestCharacterization_RunWorker_StopNoGoroutineLeak(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 0 // unlimited

	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	before := runtime.NumGoroutine()

	done := make(chan struct{})
	go func() {
		p.runWorker(target, id, 5*time.Millisecond, 200*time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	p.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runWorker did not stop after Close()")
	}

	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before {
		t.Errorf("goroutine leak after runWorker stopped: before=%d after=%d", before, after)
	}
}

// TestCharacterization_RunWorker_CountMultiSendCSV pins the seq numbering
// (1, 2, 3, ...) and per-probe CSV lines across a multi-probe Count run, all
// replies succeeding.
func TestCharacterization_RunWorker_CountMultiSendCSV(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	resolve := func(network, address string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, nil
	}
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddr: resolve})
	p.connV4 = &fakePacketConn{}
	p.Count = 3

	var logBuf bytes.Buffer
	p.LogWriter = &logBuf

	id := p.baseID & 0xffff
	ch := make(chan Reply, 3)
	p.targetChans[id] = ch

	go func() {
		for seq := 1; seq <= 3; seq++ {
			ch <- Reply{TTL: 64, Seq: seq}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	p.runWorker(target, id, 10*time.Millisecond, 200*time.Millisecond)

	view := target.GetView()
	if view.Sent != 3 {
		t.Fatalf("Sent = %d, want 3", view.Sent)
	}
	if view.Recv != 3 {
		t.Fatalf("Recv = %d, want 3", view.Recv)
	}

	records := csvFields(t, logBuf.String())
	if len(records) != 3 {
		t.Fatalf("log records = %d, want 3: %q", len(records), logBuf.String())
	}
	for i, rec := range records {
		wantSeq := strconv.Itoa(i + 1)
		if rec[3] != wantSeq {
			t.Fatalf("record %d seq column = %q, want %q", i, rec[3], wantSeq)
		}
		if rec[4] != "OK" {
			t.Fatalf("record %d status column = %q, want %q", i, rec[4], "OK")
		}
	}
}
