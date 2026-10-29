package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
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
			awaitTarget(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
			target.Reset()
			switch outcome {
			case "success":
				ch <- Reply{Seq: 1, TTL: 64}
			case "icmp error":
				ch <- Reply{Seq: 1, Err: "unreachable"}
			}
			<-done
			v := target.GetView()
			if v.Sent != 0 || v.Recv != 0 || v.Loss != 0 || len(v.History) != 0 {
				t.Fatalf("pre-reset result counted: %+v", v)
			}
		})
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
	awaitTarget(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
	target.Reset()
	ch <- Reply{Seq: 1, TTL: 64}
	awaitTarget(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
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

func TestResetRejectsPreviousDuplicateAndLateReply(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprint("late=", late), func(t *testing.T) {
			target := stats.NewTargetStats("127.0.0.1")
			p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
			p.connV4 = &fakePacketConn{}
			ch := make(chan Reply, 2)
			p.targetChans[p.baseID] = ch
			done := make(chan struct{})
			go func() { p.runWorker(target, p.baseID, time.Hour, 30*time.Millisecond); close(done) }()
			t.Cleanup(func() { p.Stop(); <-done })
			awaitTarget(t, target, func(v stats.TargetView) bool { return v.Sent == 1 })
			if late {
				awaitTarget(t, target, func(v stats.TargetView) bool { return v.Loss == 1 })
			} else {
				ch <- Reply{Seq: 1, TTL: 64}
				awaitTarget(t, target, func(v stats.TargetView) bool { return v.Recv == 1 })
			}
			target.Reset()
			// The old reply, then a marker for a seq never sent: the worker
			// takes replies one at a time, so once the marker has left the
			// channel the old reply has been classified.
			ch <- Reply{Seq: 1, TTL: 64}
			ch <- Reply{Seq: 999, TTL: 64}
			deadline := time.Now().Add(time.Second)
			for len(ch) > 0 {
				if time.Now().After(deadline) {
					t.Fatal("worker did not take the replies")
				}
				time.Sleep(time.Millisecond)
			}
			if v := target.GetView(); v.Sent != 0 || v.Recv != 0 || v.Loss != 0 || v.Duplicates != 0 || v.LateReplies != 0 {
				t.Fatalf("old history changed reset stats: %+v", v)
			}
		})
	}
}

func TestResetDuringSendDoesNotRecordOldSendOrFailure(t *testing.T) {
	for _, writeErr := range []error{nil, errors.New("write failed")} {
		t.Run(fmt.Sprint(writeErr), func(t *testing.T) {
			target := stats.NewTargetStats("127.0.0.1")
			p := NewPinger([]*stats.TargetStats{target})
			entered, release := make(chan struct{}), make(chan struct{})
			// The write blocks until the test has reset the target, then
			// completes (successfully or not) as a send that was in flight.
			p.connV4 = &fakePacketConn{onWrite: func([]byte, net.Addr) error {
				close(entered)
				<-release
				return writeErr
			}}
			done := make(chan struct{})
			go func() {
				p.sendProbe(target, p.baseID, 1, buildPayload(56), &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)})
				close(done)
			}()
			<-entered
			target.Reset()
			close(release)
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
