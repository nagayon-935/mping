package pinger

import (
	"encoding/binary"
	"time"
)

// procStart anchors embedded send timestamps to process start rather than
// wall-clock time. A wall-clock timestamp serialized into a probe payload
// would be corrupted by NTP adjustments or a DST transition occurring while
// the probe is in flight; expressing the timestamp as an offset from
// procStart and reading it back via time.Since/Sub instead relies on the
// monotonic clock reading Go carries inside every time.Time obtained from
// time.Now(), which such wall-clock changes never affect.
var procStart = time.Now()

// embedSendTimestamp writes `sent`, as nanoseconds elapsed since procStart,
// into payload immediately after the MPING signature (see
// minTimestampPayloadSize). The receiver goroutine reads it back via
// extractSendTimestamp to compute RTT without an extra hop through
// runWorker's own goroutine (see handleEchoReply). It is a no-op when
// payload is smaller than minTimestampPayloadSize (e.g. -s below 13),
// leaving that probe's RTT to runWorker's own start-time fallback.
func embedSendTimestamp(payload []byte, sent time.Time) {
	if len(payload) < minTimestampPayloadSize {
		return
	}
	elapsed := uint64(sent.Sub(procStart))
	binary.BigEndian.PutUint64(payload[timestampOffset:timestampOffset+timestampSize], elapsed)
}

// extractSendTimestamp reads the timestamp embedSendTimestamp wrote into an
// echo reply's payload and returns the RTT it implies, measured against
// `received`. ok is false whenever the value can't be trusted: the payload
// is too small to carry both signature and timestamp, the signature bytes
// don't match (a middlebox rewrote the payload in transit, or this isn't
// one of our probes), or isPlausibleRTT rejects the resulting duration.
// Callers must fall back to their own RTT bookkeeping when ok is false.
func extractSendTimestamp(data []byte, received time.Time, timeout time.Duration) (time.Duration, bool) {
	if len(data) < minTimestampPayloadSize {
		return 0, false
	}
	if string(data[:len(payloadSignature)]) != payloadSignature {
		return 0, false
	}
	elapsed := binary.BigEndian.Uint64(data[timestampOffset : timestampOffset+timestampSize])
	sentAt := procStart.Add(time.Duration(elapsed))
	rtt := received.Sub(sentAt)
	if !isPlausibleRTT(rtt, timeout) {
		return 0, false
	}
	return rtt, true
}

// isPlausibleRTT sanity-checks an RTT computed from a payload-embedded
// timestamp before it's trusted, because that payload travels through
// equipment mping doesn't control — some NAT gateways and consumer routers
// are known to rewrite ICMP echo payload bytes in transit. Two conditions
// prove the bytes were corrupted rather than reflecting genuine network
// behavior:
//
//   - rtt < 0: impossible under a monotonic clock (time.Time.Sub between
//     two monotonic readings never goes backwards), so a negative result
//     can only come from mangled timestamp bytes.
//   - rtt > 2*timeout: a reply arriving this late would already have had
//     its `unacked` entry swept out as a timeout before runWorker's select
//     loop could ever match it against a still-live seq (see
//     rearmSweepTimer), so a *genuine* embedded timestamp can never
//     legitimately produce a value this large. The 2x margin (rather than
//     exactly `timeout`) only absorbs scheduling/clock-read skew between
//     the sweep firing and this reply being processed — it does not weaken
//     the corruption check, since anything past one full timeout is
//     already unreachable via the normal path. timeout<=0 disables this
//     half of the check (not reachable via Start() in production, but
//     guards direct/test callers that don't set one).
func isPlausibleRTT(rtt, timeout time.Duration) bool {
	if rtt < 0 {
		return false
	}
	if timeout > 0 && rtt > 2*timeout {
		return false
	}
	return true
}
