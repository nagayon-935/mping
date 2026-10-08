package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/ipv4"
)

func TestResetRejectsOutstandingProbeResults(t *testing.T) {
	for _, outcome := range []string{"success", "icmp error", "timeout"} {
		t.Run(outcome, func(t *testing.T) {
			target := stats.NewTargetStats("127.0.0.1")
			p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
				return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil
			}})
			p.connV4 = &fakePacketConn{}
			p.Count = 1
			ch := make(chan Reply, 1)
			p.targetChans[p.baseID] = ch
			done := make(chan struct{})
			go func() { p.runWorker(target, p.baseID, time.Second, 200*time.Millisecond); close(done) }()
			t.Cleanup(func() { p.Stop(); <-done })
			deadline := time.Now().Add(time.Second)
			for target.GetView().Sent != 1 {
				if time.Now().After(deadline) {
					t.Fatal("probe not sent")
				}
				time.Sleep(time.Millisecond)
			}
			target.Reset()
			switch outcome {
			case "success":
				ch <- Reply{Seq: 1, TTL: 64, DSCP: 184}
			case "icmp error":
				ch <- Reply{Seq: 1, Err: "unreachable"}
			}
			<-done
			v := target.GetView()
			if v.Sent != 0 || v.Recv != 0 || v.Loss != 0 || v.LastDSCP != 0 || len(v.History) != 0 {
				t.Fatalf("pre-reset result counted: %+v", v)
			}
		})
	}
}

func TestParsePortSpecRejectsTrailingCharacters(t *testing.T) {
	for _, raw := range []string{"443garbage/tcp", "443.5", "443:80", "80 90/tcp", "53x/udp"} {
		if spec, err := ParsePortSpec(raw); err == nil {
			t.Errorf("accepted %q as %+v", raw, spec)
		}
	}
}

// The worker has its own probe budget. Reset opens a new statistics window
// while letting that worker finish the remaining sends.
func TestResetKeepsRemainingCountBudget(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
		return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil
	}})
	p.connV4 = &fakePacketConn{}
	p.Count = 2
	ch := make(chan Reply, 2)
	p.targetChans[p.baseID] = ch
	done := make(chan struct{})
	go func() { p.runWorker(target, p.baseID, 50*time.Millisecond, time.Second); close(done) }()
	t.Cleanup(func() { p.Stop(); <-done })
	waitResetView(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
	target.Reset()
	ch <- Reply{Seq: 1, TTL: 64}
	waitResetView(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
	ch <- Reply{Seq: 2, TTL: 64}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reset renewed the count budget")
	}
	v := target.GetView()
	if v.Sent != 1 || v.Recv != 1 || v.Loss != 0 {
		t.Fatalf("wrong post-reset count: %+v", v)
	}
}

func waitResetView(t *testing.T, target *stats.TargetStats, ready func(stats.TargetView) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !ready(target.GetView()) {
		if time.Now().After(deadline) {
			t.Fatal("worker did not reach expected statistics")
		}
		time.Sleep(time.Millisecond)
	}
}

type resetLogWriter chan string

func (w resetLogWriter) Write(data []byte) (int, error) { w <- string(data); return len(data), nil }

func TestResetRejectsPreviousDuplicateAndLateReply(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprint("late=", late), func(t *testing.T) {
			target := stats.NewTargetStats("127.0.0.1")
			p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
			p.connV4 = &fakePacketConn{}
			ch := make(chan Reply, 2)
			p.targetChans[p.baseID] = ch
			logs := make(resetLogWriter, 8)
			p.LogWriter = logs
			done := make(chan struct{})
			go func() { p.runWorker(target, p.baseID, time.Hour, 30*time.Millisecond); close(done) }()
			t.Cleanup(func() { p.Stop(); <-done })
			waitResetView(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
			want := ",DUP,"
			if late {
				waitResetView(t, target, func(v stats.TargetView) bool { return v.Loss == 1 })
				want = ",LateReply,"
			} else {
				ch <- Reply{Seq: 1, TTL: 64}
				waitResetView(t, target, func(v stats.TargetView) bool { return v.Recv == 1 })
			}
			target.Reset()
			ch <- Reply{Seq: 1, TTL: 64}
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			for {
				select {
				case line := <-logs:
					if strings.Contains(line, want) {
						v := target.GetView()
						if v.Sent != 0 || v.Recv != 0 || v.Loss != 0 || v.Duplicates != 0 || v.LateReplies != 0 {
							t.Fatalf("old history changed reset stats: %+v", v)
						}
						return
					}
				case <-timer.C:
					t.Fatal("worker did not classify old reply")
				}
			}
		})
	}
}

type resetWriteConn struct {
	fakePacketConn
	entered, release chan struct{}
	err              error
}

func (c *resetWriteConn) WriteTo(data []byte, _ *ipv4.ControlMessage, _ net.Addr) (int, error) {
	close(c.entered)
	<-c.release
	return len(data), c.err
}

func TestResetDuringSendDoesNotRecordOldSendOrFailure(t *testing.T) {
	for _, writeErr := range []error{nil, errors.New("write failed")} {
		t.Run(fmt.Sprint(writeErr), func(t *testing.T) {
			target := stats.NewTargetStats("127.0.0.1")
			p := NewPinger([]*stats.TargetStats{target})
			conn := &resetWriteConn{entered: make(chan struct{}), release: make(chan struct{}), err: writeErr}
			p.connV4 = conn
			done := make(chan struct{})
			go func() {
				p.sendProbe(target, p.baseID, 1, buildPayload(56), &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)})
				close(done)
			}()
			<-conn.entered
			target.Reset()
			close(conn.release)
			<-done
			if v := target.GetView(); v.Sent != 0 || v.Loss != 0 {
				t.Fatalf("old send counted after reset: %+v", v)
			}
		})
	}
}

func TestResetDuringDNSDoesNotRecordOldFailure(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	entered, release := make(chan struct{}), make(chan struct{})
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
		close(entered)
		<-release
		return nil, errors.New("DNS failed")
	}})
	done := make(chan struct{})
	go func() { p.resolveTarget(target); close(done) }()
	<-entered
	target.Reset()
	close(release)
	<-done
	if v := target.GetView(); v.Loss != 0 || v.LastError != "" {
		t.Fatalf("old DNS failure counted: %+v", v)
	}
}
