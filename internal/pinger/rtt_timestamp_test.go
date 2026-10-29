package pinger

// Tests for the payload-embedded send timestamp that lets RTT be computed in
// the receiver goroutine (handleEchoReply) instead of after an extra hop
// through runWorker's own goroutine. See embedSendTimestamp,
// extractSendTimestamp, and isPlausibleRTT in pinger.go.

import (
	"net"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
)

// TestEmbedAndExtractSendTimestamp_RoundTrip pins that a timestamp embedded
// by embedSendTimestamp is read back by extractSendTimestamp as the exact
// RTT between the two instants supplied.
func TestEmbedAndExtractSendTimestamp_RoundTrip(t *testing.T) {
	payload := buildPayload(56)
	sent := time.Now()
	embedSendTimestamp(payload, sent)

	received := sent.Add(37 * time.Millisecond)
	rtt, ok := extractSendTimestamp(payload, received, time.Second)
	if !ok {
		t.Fatal("expected extraction to succeed")
	}
	if rtt != 37*time.Millisecond {
		t.Fatalf("rtt = %v, want %v", rtt, 37*time.Millisecond)
	}
}

// TestEmbedAndExtractSendTimestamp_SignaturePreserved pins that embedding a
// timestamp never disturbs the MPING signature packet captures rely on.
func TestEmbedAndExtractSendTimestamp_SignaturePreserved(t *testing.T) {
	payload := buildPayload(56)
	embedSendTimestamp(payload, time.Now())

	if string(payload[:5]) != payloadSignature {
		t.Fatalf("signature = %q, want %q", string(payload[:5]), payloadSignature)
	}
}

// TestExtractSendTimestamp_SizeBoundary pins the exact -s boundary at which
// timestamp embedding becomes possible: signature (5 bytes) + timestamp (8
// bytes) = 13 bytes. Below that, extraction must fail (fallback territory);
// at or above it, extraction must succeed.
func TestExtractSendTimestamp_SizeBoundary(t *testing.T) {
	tests := []struct {
		name string
		size int
		want bool
	}{
		{"zero", 0, false},
		{"signature only, 5 bytes", 5, false},
		{"12 bytes, one short", 12, false},
		{"13 bytes, exact minimum", 13, true},
		{"14 bytes, above minimum", 14, true},
		{"56 bytes, default size", 56, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := buildPayload(tt.size)
			sent := time.Now()
			embedSendTimestamp(payload, sent)

			_, ok := extractSendTimestamp(payload, sent.Add(time.Millisecond), time.Second)
			if ok != tt.want {
				t.Fatalf("size=%d: extraction ok = %v, want %v", tt.size, ok, tt.want)
			}
		})
	}
}

// TestExtractSendTimestamp_SignatureMismatch pins that a payload whose
// leading bytes no longer match payloadSignature (e.g. rewritten by a
// middlebox in transit) is rejected outright, never partially trusted.
func TestExtractSendTimestamp_SignatureMismatch(t *testing.T) {
	payload := buildPayload(56)
	embedSendTimestamp(payload, time.Now())
	copy(payload, "XXXXX") // corrupt the signature only

	_, ok := extractSendTimestamp(payload, time.Now(), time.Second)
	if ok {
		t.Fatal("expected extraction to fail when signature is corrupted")
	}
}

// TestIsPlausibleRTT covers the two conditions that must reject a
// payload-embedded RTT as corrupted: negative (impossible under a monotonic
// clock) and more than 2x the configured timeout (unreachable via the
// normal matched-seq path, since such a reply would already have been swept
// out of `unacked` as a timeout).
func TestIsPlausibleRTT(t *testing.T) {
	tests := []struct {
		name    string
		rtt     time.Duration
		timeout time.Duration
		want    bool
	}{
		{"typical positive rtt", 20 * time.Millisecond, time.Second, true},
		{"zero rtt", 0, time.Second, true},
		{"negative rtt rejected", -1 * time.Millisecond, time.Second, false},
		{"exactly 2x timeout accepted", 2 * time.Second, time.Second, true},
		{"just over 2x timeout rejected", 2*time.Second + time.Nanosecond, time.Second, false},
		{"far over timeout rejected", 10 * time.Second, time.Second, false},
		{"timeout<=0 disables upper bound", 10 * time.Hour, 0, true},
		{"timeout<=0 still rejects negative", -time.Millisecond, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPlausibleRTT(tt.rtt, tt.timeout)
			if got != tt.want {
				t.Fatalf("isPlausibleRTT(%v, %v) = %v, want %v", tt.rtt, tt.timeout, got, tt.want)
			}
		})
	}
}

// TestExtractSendTimestamp_ImplausibleRTTRejected exercises the sanity
// check end-to-end through extractSendTimestamp, not just isPlausibleRTT in
// isolation: a timestamp that decodes to a "sent in the future" instant (as
// a middlebox flipping bits might produce) must be rejected rather than
// yielding a negative RTT.
func TestExtractSendTimestamp_ImplausibleRTTRejected(t *testing.T) {
	payload := buildPayload(56)
	future := time.Now().Add(time.Hour)
	embedSendTimestamp(payload, future) // "sent" after "received" below

	_, ok := extractSendTimestamp(payload, time.Now(), time.Second)
	if ok {
		t.Fatal("expected extraction to reject a negative RTT")
	}
}

// ---- sendProbe embedding ----

// TestSendProbe_EmbedsTimestampInPayload pins that sendProbe writes a valid,
// extractable send timestamp into the wire payload for a size large enough
// to carry one.
func TestSendProbe_EmbedsTimestampInPayload(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
	conn := &fakePacketConn{}
	p.connV4 = conn

	payload := buildPayload(56)
	start, ok := p.sendProbe(target, 1, 1, payload, &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)})
	if !ok {
		t.Fatal("expected sendProbe to succeed")
	}
	written := conn.writes()
	if len(written) != 1 {
		t.Fatalf("written packets = %d, want 1", len(written))
	}
	echo := icmpEchoOf(t, written[0])

	rtt, ok := extractSendTimestamp(echo.Data, start, time.Second)
	if !ok {
		t.Fatal("expected the wire payload to carry an extractable timestamp")
	}
	if rtt < 0 || rtt > time.Second {
		t.Fatalf("rtt from wire payload = %v, want a small non-negative value", rtt)
	}
}

// TestSendProbe_SmallPayloadNoTimestamp pins that a payload too small to
// carry a timestamp (-s below 13) is sent as-is, without panicking, and
// without producing an extractable timestamp on the receiving end.
func TestSendProbe_SmallPayloadNoTimestamp(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
	conn := &fakePacketConn{}
	p.connV4 = conn

	payload := buildPayload(8) // signature fits (5 bytes), timestamp (8 more) does not
	_, ok := p.sendProbe(target, 1, 1, payload, &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)})
	if !ok {
		t.Fatal("expected sendProbe to succeed")
	}

	echo := icmpEchoOf(t, conn.writes()[0])
	if len(echo.Data) != 8 {
		t.Fatalf("payload len on wire = %d, want 8 (unchanged)", len(echo.Data))
	}
	if _, ok := extractSendTimestamp(echo.Data, time.Now(), time.Second); ok {
		t.Fatal("expected no extractable timestamp for an 8-byte payload")
	}
}

// ---- handleEchoReply integration ----

// TestHandleEchoReply_SetsRTTFromEmbeddedTimestamp pins that handleEchoReply
// populates Reply.RTT from a valid embedded timestamp, using p.now() (not
// wall-clock time.Now) as the "received" instant so the test is
// deterministic.
func TestHandleEchoReply_SetsRTTFromEmbeddedTimestamp(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	sent := time.Now()
	received := sent.Add(12 * time.Millisecond)

	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{
		Now: func() time.Time { return received },
	})
	p.probeTimeout = time.Second
	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	payload := buildPayload(56)
	embedSendTimestamp(payload, sent)

	msg := &icmp.Message{Body: &icmp.Echo{ID: id, Seq: 5, Data: payload}}
	p.handleEchoReply(msg, 64, 0)

	select {
	case reply := <-p.targetChans[id]:
		if reply.RTT != 12*time.Millisecond {
			t.Fatalf("reply.RTT = %v, want %v", reply.RTT, 12*time.Millisecond)
		}
	default:
		t.Fatal("expected a reply in channel")
	}
}

// TestHandleEchoReply_FallsBackWhenTimestampInvalid pins that Reply.RTT
// stays at its zero value (signaling "no trustworthy embedded RTT") when
// the payload's signature has been corrupted in transit, so runWorker knows
// to fall back to its own start-time bookkeeping.
func TestHandleEchoReply_FallsBackWhenTimestampInvalid(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
	p.probeTimeout = time.Second
	id := p.baseID & 0xffff
	p.targetChans[id] = make(chan Reply, 1)

	payload := buildPayload(56)
	embedSendTimestamp(payload, time.Now())
	copy(payload, "XXXXX") // corrupt signature, as a middlebox rewrite would

	msg := &icmp.Message{Body: &icmp.Echo{ID: id, Seq: 5, Data: payload}}
	p.handleEchoReply(msg, 64, 0)

	select {
	case reply := <-p.targetChans[id]:
		if reply.RTT != 0 {
			t.Fatalf("reply.RTT = %v, want 0 (fallback signal)", reply.RTT)
		}
	default:
		t.Fatal("expected a reply in channel")
	}
}

// ---- runWorker fallback wiring ----

// TestRunWorker_UsesEmbeddedRTTWhenValid pins that runWorker records the
// receiver-computed reply.RTT verbatim rather than re-deriving RTT from its
// own pend.start bookkeeping. The reply is delivered the instant the probe is
// written, so a start-time-derived RTT would be microseconds, far from the
// distinctive embedded value.
func TestRunWorker_UsesEmbeddedRTTWhenValid(t *testing.T) {
	r := newWorkerRig(t, 1)
	const embeddedRTT = 3 * time.Millisecond
	r.replyOnWrite(1, Reply{TTL: 64, Seq: 1, RTT: embeddedRTT})

	r.run(10*time.Millisecond, 500*time.Millisecond)

	if view := r.target.GetView(); view.LastRTT != embeddedRTT {
		t.Fatalf("LastRTT = %v, want the embedded %v (runWorker must prefer reply.RTT)", view.LastRTT, embeddedRTT)
	}
}

// TestRunWorker_FallsBackToStartTimeWhenRTTZero pins that runWorker still
// derives RTT from pend.start when reply.RTT is left at its zero value
// (the fallback signal from handleEchoReply), as happens for small
// payloads / untrusted timestamps.
func TestRunWorker_FallsBackToStartTimeWhenRTTZero(t *testing.T) {
	r := newWorkerRig(t, 1)
	// The probe's start time is taken just before WriteTo, so a reply
	// delivered 20ms into the write guarantees a fallback RTT of at least 20ms.
	r.conn.onWrite = func([]byte, net.Addr) error {
		time.Sleep(20 * time.Millisecond)
		r.ch <- Reply{TTL: 64, Seq: 1} // RTT left zero
		return nil
	}

	r.run(10*time.Millisecond, 500*time.Millisecond)

	if view := r.target.GetView(); view.LastRTT < 20*time.Millisecond {
		t.Fatalf("LastRTT = %v, want >= ~20ms (fallback to start-time bookkeeping)", view.LastRTT)
	}
}
