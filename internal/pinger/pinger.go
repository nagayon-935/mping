package pinger

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	receiverBufferSize  = 65535                  // max IPv4 packet size; large enough for any ICMP message
	probeBufferSize     = 1500                   // typical Ethernet MTU; sufficient for PMTU probe responses
	replyChanBuffer     = 100                    // allow burst of replies without blocking the receiver
	traceChanBuffer     = 200                    // larger buffer for concurrent TraceRoute calls
	receiverReadTimeout = 1 * time.Second        // poll interval for checking done channel in receiver loop
	pmtuProbeTimeout    = 300 * time.Millisecond // generous enough for WAN RTTs, short enough for interactive use
	payloadSignature    = "MPING"                // identifies our probes in packet captures
	traceSignature      = "TRC-"                 // 4-byte prefix distinguishing traceroute probes from ping probes

	// seqMask masks a logical (unbounded) seq counter down to the 16-bit
	// range the ICMP echo Seq field occupies on the wire: icmp.Echo.Marshal
	// truncates via uint16(p.Seq), and icmp.ParseMessage never returns a Seq
	// outside [0, 65535]. runWorker's seq counter is a plain int that grows
	// forever, so it must be masked both when building the outgoing Echo
	// and when matching an incoming reply — otherwise, once the counter
	// exceeds 65535, replies (correctly wrapped by the peer) never compare
	// equal to it again and every subsequent probe times out permanently.
	seqMask = 0xffff

	asnJitterMax = 2 * time.Second // spreads concurrent targets' ASN lookups to avoid bursting Cymru's public server
	ptrJitterMax = 2 * time.Second // spreads concurrent targets' PTR lookups to avoid bursting the configured resolver

	// timestampSize is the width, in bytes, of the send timestamp embedded
	// in a probe's payload: an 8-byte big-endian count of nanoseconds
	// elapsed since procStart.
	timestampSize = 8

	// timestampOffset is where the embedded send timestamp begins within a
	// probe payload. It sits immediately after payloadSignature, which must
	// stay first since packet captures rely on it at a fixed offset to
	// identify our probes.
	timestampOffset = len(payloadSignature)

	// minTimestampPayloadSize is the smallest -s payload size with room for
	// both payloadSignature and the embedded timestamp (5 + 8 = 13 bytes).
	// Below it, embedSendTimestamp/extractSendTimestamp are no-ops and
	// runWorker falls back to its own start-time bookkeeping in `unacked`.
	minTimestampPayloadSize = timestampOffset + timestampSize
)

// asnLookupTimeout bounds each Cymru DNS TXT query, since neither net.LookupTXT
// nor a *net.Resolver wrapped with context.Background() has a timeout of its
// own. A var (not const) so tests can shrink it instead of waiting 3s.
var asnLookupTimeout = 3 * time.Second

// ptrLookupTimeout bounds each PTR (reverse DNS) query, since neither
// net.LookupAddr nor a *net.Resolver wrapped with context.Background() has a
// timeout of its own. A var (not const) so tests can shrink it instead of
// waiting 3s. Mirrors asnLookupTimeout.
var ptrLookupTimeout = 3 * time.Second

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

// errPingerStopped is returned by the bounded lookup helpers when Stop()
// closed p.done while a call was in flight. Callers use it to distinguish
// "we are shutting down" from a genuine DNS failure, so shutdown doesn't
// record a spurious loss against the target.
var errPingerStopped = errors.New("pinger stopped")

// resolveTimeout bounds each target DNS resolution. resolveIPAddr takes no
// context, and neither net.ResolveIPAddr nor a *net.Resolver driven with
// context.Background() has a timeout of its own — an unresponsive
// --dns-server was measured blocking ~40s. A var (not const) so tests can
// shrink it, matching asnLookupTimeout.
var resolveTimeout = 5 * time.Second

// PacketConnV4 interface matches *ipv4.PacketConn methods we use
type PacketConnV4 interface {
	ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error)
	WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error)
	SetReadDeadline(t time.Time) error
	Close() error
	SetControlMessage(cf ipv4.ControlFlags, on bool) error
	// SetTOS arms the socket-wide default outbound DSCP/ECN byte
	// (Pinger.DSCP). Unlike PacketConnV6.SetTrafficClass, this is IPv4's
	// ONLY DSCP hook: x/net's ipv4.ControlMessage carries no TOS field, so
	// there is no per-packet write-side override and no receive-side
	// observation for IPv4 (both of which ipv6.ControlMessage supports via
	// TrafficClass) — every IPv4 target is stuck sharing this one global
	// value. A Pinger.TargetDSCP entry for an IPv4 target is therefore
	// silently unusable; see getWriteFunc.
	SetTOS(tos int) error
}

// PacketConnV6 interface matches *ipv6.PacketConn methods we use
type PacketConnV6 interface {
	ReadFrom(b []byte) (int, *ipv6.ControlMessage, net.Addr, error)
	WriteTo(b []byte, cm *ipv6.ControlMessage, dst net.Addr) (int, error)
	SetReadDeadline(t time.Time) error
	Close() error
	SetControlMessage(cf ipv6.ControlFlags, on bool) error
	SetICMPFilter(f *ipv6.ICMPFilter) error
	// SetTrafficClass is SetTOS's IPv6 counterpart (see PacketConnV4.SetTOS).
	SetTrafficClass(tc int) error
}

// Reply represents a single received ICMP echo reply or error from the receiver loop.
type Reply struct {
	// RTT is computed in the receiver goroutine from the timestamp
	// embedSendTimestamp wrote into the probe's own payload (see
	// handleEchoReply/extractSendTimestamp), avoiding the scheduling delay
	// of also crossing into runWorker's goroutine to compute it there. It
	// is left at its zero value whenever that timestamp couldn't be
	// trusted — payload too small for -s, signature mismatch, or a
	// suspicious result caught by isPlausibleRTT — in which case runWorker
	// falls back to its own start-time bookkeeping in `unacked`.
	RTT time.Duration
	TTL int
	// DSCP is the TOS (IPv4) / TrafficClass (IPv6) byte read off the reply's
	// own IP header via the ipv4.FlagTOS / ipv6.FlagTrafficClass control
	// message — i.e. what the reply actually arrived carrying, after any
	// re-marking or bleaching along the return path. Zero when the platform
	// didn't supply a control message, indistinguishable from an explicit
	// CS0/Default reply; this mirrors the pre-existing TTL field's same
	// zero-value ambiguity.
	DSCP int
	Seq  int
	Err  string
}

type traceMsg struct {
	parsed *icmp.Message
	src    net.Addr
}

type Pinger struct {
	Targets []*stats.TargetStats

	Source string // Source IP address to bind to

	// Interface is the network interface name requested via -I. When set,
	// Start() and OpenHopSocket() additionally bind each socket to this
	// physical interface (SO_BINDTODEVICE on Linux, IP_BOUND_IF/
	// IPV6_BOUND_IF on Darwin — see bindif_linux.go/bindif_darwin.go),
	// on top of the source-IP bind Source already performs. This enforces
	// the egress interface even when the routing table would otherwise
	// choose a different one, or when the same address is assigned to more
	// than one interface. It has no effect on platforms without a
	// supported binding facility (bindif_other.go), where source-IP bind
	// via Source remains the only interface-selection mechanism, exactly
	// matching pre-existing behavior.
	Interface string

	Size  int // Payload size in bytes
	Count int // Stop after sending Count packets (0 = infinite)

	ResolveInterval time.Duration // Interval to re-resolve DNS
	AsnEnabled      bool          // Enable ASN lookups
	PtrEnabled      bool          // Enable PTR (reverse DNS) lookups

	// DSCP is the outbound DSCP-derived TOS (IPv4) / TrafficClass (IPv6)
	// byte applied to every target via a socket-wide SetTOS/SetTrafficClass
	// call in Start(). dscpUnset (the default) leaves the OS default in
	// place. A per-target entry in TargetDSCP overrides this for that one
	// target.
	DSCP int

	// TargetDSCP holds a per-target override of the outbound DSCP byte,
	// keyed by stats.TargetStats.Host (the same string cmd/main's
	// targetSpec.display() produces, matching how Targets was built).
	// Because every target shares one underlying socket, a per-target
	// override can't use SetTOS/SetTrafficClass (which is socket-wide);
	// sendProbe instead attaches an explicit per-packet
	// ipv4.ControlMessage{TOS: ...} / ipv6.ControlMessage{TrafficClass: ...}
	// to just that target's WriteTo calls, leaving DSCP's socket-wide
	// default (or the OS default when DSCP is unset) in place for every
	// other target.
	TargetDSCP map[string]int

	connV4      PacketConnV4
	connV6      PacketConnV6
	targetMap   map[int]*stats.TargetStats
	targetChans map[int]chan Reply
	mapMu       sync.RWMutex
	baseID      int

	// probeTimeout mirrors the timeout passed to Start, kept so
	// handleEchoReply's isPlausibleRTT sanity check has a timeout to
	// compare a payload-embedded RTT against. handleEchoReply runs in the
	// receiver goroutine, which has no per-worker context of its own, so
	// this single Pinger-wide value is the only timeout it can reference —
	// matching Start's existing single timeout parameter already shared by
	// every worker.
	probeTimeout time.Duration

	// initialSeq is the value runWorker's logical seq counter starts from
	// (before the first seq++). Always 0 in production; tests override it
	// via Options.InitialSeq to reach the 16-bit wraparound boundary
	// without waiting for 65535 real probes.
	initialSeq int

	asnCache  map[string]ASNInfo
	asnMu     sync.RWMutex
	asnJitter func() time.Duration // returns a random delay to stagger concurrent Cymru lookups; overridden to 0 in tests

	ptrCache  map[string]string
	ptrMu     sync.RWMutex
	ptrJitter func() time.Duration // returns a random delay to stagger concurrent PTR lookups; overridden to 0 in tests

	traceChans   map[int]chan traceMsg // keyed by trace ID
	traceChansMu sync.RWMutex
	traceCounter atomic.Uint32 // unique traceID per concurrent call

	LogWriter io.Writer // Optional logger

	done     chan struct{} // Signal to close receiver
	stopOnce sync.Once     // guards close(done); Stop may be called concurrently
	wg       sync.WaitGroup

	resolveIPAddr resolveIPAddrFunc
	now           func() time.Time
	listenPacket  listenPacketFunc
	lookupTXT     func(string) ([]string, error)
	lookupAddr    func(string) ([]string, error)
}

type resolveIPAddrFunc func(network, address string) (*net.IPAddr, error)

type listenPacketFunc func(network, address string) (net.PacketConn, error)

// bindToInterfaceFn is a seam over the platform-specific bindToInterface
// implementation (bindif_linux.go / bindif_darwin.go / bindif_other.go),
// letting tests verify that Start() and OpenHopSocket() dispatch to it with
// the correct interface name and address family without requiring root
// privileges, real sockets, or real network interfaces.
var bindToInterfaceFn = bindToInterface

type Options struct {
	ResolveIPAddr resolveIPAddrFunc
	Resolver      *net.Resolver
	Now           func() time.Time
	ListenPacket  listenPacketFunc
	LookupTXT     func(string) ([]string, error)
	LookupAddr    func(string) ([]string, error)
	AsnEnabled    bool
	PtrEnabled    bool

	// InitialSeq overrides the starting value of runWorker's logical seq
	// counter. Zero value matches production behavior (start at 0); tests
	// set it to exercise the 16-bit ICMP seq wraparound boundary.
	InitialSeq int

	// DSCP is the global outbound DSCP-derived TOS/TrafficClass byte
	// (0-255). Nil (the zero value) leaves the OS default in place — a
	// *int rather than a plain int so "not configured" is distinguishable
	// from an explicit "0" (CS0/Default) selection.
	DSCP *int

	// TargetDSCP overrides DSCP for specific targets, keyed by target Host
	// (matching stats.TargetStats.Host / targetSpec.display()). See
	// Pinger.TargetDSCP for why this needs per-packet handling instead of a
	// socket-wide call.
	TargetDSCP map[string]int
}

// NewPinger creates a Pinger with default options for the given targets.
// Production code always goes through NewPingerWithOptions (to inject
// resolver/listener test doubles); this constructor exists for tests that
// don't need to override any Options.
func NewPinger(targets []*stats.TargetStats) *Pinger {
	return NewPingerWithOptions(targets, Options{})
}

// NewPingerWithOptions creates a Pinger with the provided options.
func NewPingerWithOptions(targets []*stats.TargetStats, opts Options) *Pinger {
	resolve := opts.ResolveIPAddr
	if resolve == nil {
		if opts.Resolver != nil {
			resolve = func(network, address string) (*net.IPAddr, error) {
				ips, err := opts.Resolver.LookupIP(context.Background(), network, address)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, &net.DNSError{Err: "no such host", Name: address}
				}
				return &net.IPAddr{IP: ips[0]}, nil
			}
		} else {
			resolve = net.ResolveIPAddr
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	listen := opts.ListenPacket
	if listen == nil {
		listen = net.ListenPacket
	}
	lookup := opts.LookupTXT
	if lookup == nil {
		if opts.Resolver != nil {
			lookup = func(name string) ([]string, error) {
				return opts.Resolver.LookupTXT(context.Background(), name)
			}
		} else {
			lookup = net.LookupTXT
		}
	}
	lookupAddr := opts.LookupAddr
	if lookupAddr == nil {
		if opts.Resolver != nil {
			lookupAddr = func(ip string) ([]string, error) {
				return opts.Resolver.LookupAddr(context.Background(), ip)
			}
		} else {
			lookupAddr = net.LookupAddr
		}
	}

	dscp := dscpUnset
	if opts.DSCP != nil {
		dscp = *opts.DSCP
	}

	return &Pinger{
		Targets:         targets,
		targetMap:       make(map[int]*stats.TargetStats),
		targetChans:     make(map[int]chan Reply),
		asnCache:        make(map[string]ASNInfo),
		ptrCache:        make(map[string]string),
		baseID:          os.Getpid() & 0xffff,
		Size:            56, // Default payload size (like standard ping)
		ResolveInterval: 60 * time.Second,
		AsnEnabled:      opts.AsnEnabled,
		PtrEnabled:      opts.PtrEnabled,
		DSCP:            dscp,
		TargetDSCP:      opts.TargetDSCP,
		traceChans:      make(map[int]chan traceMsg),
		done:            make(chan struct{}),
		initialSeq:      opts.InitialSeq,
		resolveIPAddr:   resolve,
		now:             now,
		listenPacket:    listen,
		lookupTXT:       lookup,
		lookupAddr:      lookupAddr,
		asnJitter:       func() time.Duration { return time.Duration(rand.Int63n(int64(asnJitterMax))) },
		ptrJitter:       func() time.Duration { return time.Duration(rand.Int63n(int64(ptrJitterMax))) },
	}
}

func (p *Pinger) log(t *stats.TargetStats, seq int, status string, rtt time.Duration, ttl int, errMsg string) {
	if p.LogWriter == nil {
		return
	}
	// CSV format: Timestamp, Host, IP, Seq, Status, RTT(ms), TTL, Error
	timestamp := p.now().Format(time.RFC3339Nano)
	rttMs := float64(rtt.Microseconds()) / 1000.0

	line := fmt.Sprintf("%s,%s,%s,%d,%s,%.3f,%d,%s\n",
		timestamp, t.Host, t.GetView().IP, seq, status, rttMs, ttl, errMsg)

	if _, err := p.LogWriter.Write([]byte(line)); err != nil && p.LogWriter != io.Discard {
		// Best-effort logging; write errors are non-fatal but surfaced via stderr when possible.
		fmt.Fprintf(os.Stderr, "mping: log write error: %v\n", err)
	}
}

// applyLastErrSource substitutes the bound source IP into raw-socket write
// errors, which the kernel reports against 0.0.0.0 regardless of the actual
// bind address. Only fires when Source was explicitly set (-S/-I); when the
// source is auto-detected, p.Source is empty here and ui.normalizeWriteIP
// performs the equivalent substitution at display time using the UI's own
// detected source IP — the two are not redundant with each other.
func (p *Pinger) applyLastErrSource(errMsg string) string {
	if p.Source != "" && strings.Contains(errMsg, "write ip 0.0.0.0->") {
		return strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+p.Source+"->", 1)
	}
	return errMsg
}

// Stop signals the receiver and worker goroutines to exit. Safe to call
// from multiple goroutines concurrently and any number of times, matching
// PortChecker.Stop and HTTPChecker.Stop. The UI reaches this concurrently:
// the 's' key handler runs stopAll in its own goroutine while run()'s
// cleanup path calls stopAll on the main goroutine.
func (p *Pinger) Stop() {
	if p.done == nil {
		return
	}
	p.stopOnce.Do(func() { close(p.done) })
}

func (p *Pinger) Start(interval, timeout time.Duration) error {
	p.probeTimeout = timeout

	var errV4, errV6 error

	// Initialize IPv4
	if p.Source == "" || isIPv4(p.Source) {
		network := "ip4:icmp"
		bindAddr := "0.0.0.0"
		if p.Source != "" {
			bindAddr = p.Source
		}

		c, err := p.listenPacket(network, bindAddr)
		if err == nil {
			// Non-fatal: true interface binding may be unsupported on this
			// platform, or fail (e.g. missing privileges); the source-IP
			// bind above still applies regardless.
			bindToInterfaceFn(c, p.Interface, false)
			p.connV4 = ipv4.NewPacketConn(c)
			// Non-fatal: TTL control message may not be available on all platforms.
			_ = p.connV4.SetControlMessage(ipv4.FlagTTL, true)
		} else {
			errV4 = err
		}
	}

	// Initialize IPv6
	if p.Source == "" || !isIPv4(p.Source) {
		network := "ip6:ipv6-icmp"
		bindAddr := "::"
		if p.Source != "" {
			bindAddr = p.Source
		}

		c, err := p.listenPacket(network, bindAddr)
		if err == nil {
			// Non-fatal, same rationale as the IPv4 block above.
			bindToInterfaceFn(c, p.Interface, true)
			p.connV6 = ipv6.NewPacketConn(c)
			// Non-fatal: hop limit/traffic class control messages may not
			// be available on all platforms.
			_ = p.connV6.SetControlMessage(ipv6.FlagHopLimit|ipv6.FlagTrafficClass, true)
			applyICMPv6Filter(p.connV6)
		} else {
			errV6 = err
		}
	}

	if p.connV4 == nil && p.connV6 == nil {
		return fmt.Errorf("failed to initialize pinger: v4=%v, v6=%v", errV4, errV6)
	}

	p.armDSCP()

	// Register targets and start workers
	for i, t := range p.Targets {
		id := (p.baseID + i) & 0xffff

		p.mapMu.Lock()
		p.targetMap[id] = t
		p.targetChans[id] = make(chan Reply, replyChanBuffer)
		p.mapMu.Unlock()

		p.wg.Add(1)
		go func(t *stats.TargetStats, id int) {
			defer p.wg.Done()
			p.runWorker(t, id, interval, timeout)
		}(t, id)
	}

	// Start Receivers
	if p.connV4 != nil {
		p.wg.Add(1)
		go func() { defer p.wg.Done(); p.runReceiverV4() }()
	}
	if p.connV6 != nil {
		p.wg.Add(1)
		go func() { defer p.wg.Done(); p.runReceiverV6() }()
	}

	return nil
}

// armDSCP applies Start()'s global DSCP default (Pinger.DSCP) to whichever
// sockets Start() just opened, via SetTOS (IPv4) / SetTrafficClass (IPv6).
// A no-op when DSCP is dscpUnset (the default), leaving the OS default TOS/
// TrafficClass in place. Split out from Start() so it can be exercised
// directly against PacketConnV4/V6 test doubles — Start() itself always
// wraps its listenPacket result in a real *ipv4.PacketConn/*ipv6.PacketConn,
// which makes SetTOS/SetTrafficClass's effect unobservable through a plain
// net.PacketConn fake.
//
// Both calls are non-fatal (same rationale as the neighboring
// SetControlMessage calls in Start()): a platform where TOS/TrafficClass
// isn't settable shouldn't abort startup over what amounts to an optional
// QoS marking.
func (p *Pinger) armDSCP() {
	if p.DSCP == dscpUnset {
		return
	}
	if p.connV4 != nil {
		_ = p.connV4.SetTOS(p.DSCP)
	}
	if p.connV6 != nil {
		_ = p.connV6.SetTrafficClass(p.DSCP)
	}
}

func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

func (p *Pinger) Wait() {
	p.wg.Wait()
}

func (p *Pinger) Close() {
	p.Stop() // reuse the idempotent done-channel guard
	if p.connV4 != nil {
		p.connV4.Close()
	}
	if p.connV6 != nil {
		p.connV6.Close()
	}
}

func extractTraceID(msg *icmp.Message) (int, bool) {
	switch msg.Type {
	case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
		if echo, ok := msg.Body.(*icmp.Echo); ok {
			return echo.ID, true
		}
	case ipv4.ICMPTypeTimeExceeded, ipv6.ICMPTypeTimeExceeded,
		ipv4.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeDestinationUnreachable:
		id, _, ok := extractEchoIDSeq(msg)
		return id, ok
	}
	return 0, false
}

func (p *Pinger) broadcastTrace(msg *icmp.Message, src net.Addr) {
	id, ok := extractTraceID(msg)
	if !ok {
		return
	}
	p.traceChansMu.RLock()
	ch, exists := p.traceChans[id]
	p.traceChansMu.RUnlock()
	if exists {
		select {
		case ch <- traceMsg{msg, src}:
		default:
		}
	}
}

// receiverConfig holds IP-version-specific parameters for the unified receiver loop.
type receiverConfig struct {
	protocol      int         // ICMP protocol number (1 for v4, 58 for v6)
	echoReply     icmp.Type   // ICMPTypeEchoReply or ICMPTypeEchoReply (v6)
	errorTypes    []icmp.Type // Destination Unreachable, Time Exceeded, Parameter Problem
	errorStringFn func(icmp.Type, int) string
}

var receiverV4Config = receiverConfig{
	protocol:      1,
	echoReply:     ipv4.ICMPTypeEchoReply,
	errorTypes:    []icmp.Type{ipv4.ICMPTypeDestinationUnreachable, ipv4.ICMPTypeTimeExceeded, ipv4.ICMPTypeParameterProblem},
	errorStringFn: icmpErrorString,
}

var receiverV6Config = receiverConfig{
	protocol:  58,
	echoReply: ipv6.ICMPTypeEchoReply,
	// ipv6.ICMPTypePacketTooBig (type 2) has no IPv4 equivalent code to piggyback
	// on - IPv4's "Fragmentation Needed" is DstUnreach code 4, but IPv6 carries the
	// same signal as its own top-level type. Omitting it here means MTU problems on
	// IPv6 are silently dropped by isErrorType and surface as plain timeouts.
	errorTypes: []icmp.Type{
		ipv6.ICMPTypeDestinationUnreachable,
		ipv6.ICMPTypePacketTooBig,
		ipv6.ICMPTypeTimeExceeded,
		ipv6.ICMPTypeParameterProblem,
	},
	errorStringFn: icmpV6ErrorString,
}

// icmpv6FilterSetter is satisfied by both PacketConnV6 (the main receiver
// socket) and hopSendConnV6 (MTR/traceroute's HopSocket). Sharing it lets
// applyICMPv6Filter serve both call sites without a second interface.
type icmpv6FilterSetter interface {
	SetICMPFilter(f *ipv6.ICMPFilter) error
}

// icmpv6FilterFromConfig builds a kernel-side ICMP6_FILTER that blocks every
// ICMPv6 type except cfg.echoReply and cfg.errorTypes. It is derived from
// cfg rather than a second, independently-maintained type list, so a future
// addition to receiverV6Config.errorTypes (e.g. another ICMP error type
// needed for traceroute/MTR) automatically widens the kernel filter too -
// there is exactly one place that enumerates the types mping cares about.
func icmpv6FilterFromConfig(cfg receiverConfig) ipv6.ICMPFilter {
	var filt ipv6.ICMPFilter
	filt.SetAll(true) // block everything by default (ICMP6_FILTER_SETBLOCKALL)
	if t, ok := cfg.echoReply.(ipv6.ICMPType); ok {
		filt.Accept(t)
	}
	for _, et := range cfg.errorTypes {
		if t, ok := et.(ipv6.ICMPType); ok {
			filt.Accept(t)
		}
	}
	return filt
}

// applyICMPv6Filter installs the kernel-side ICMPv6 filter derived from
// receiverV6Config onto conn. IPv6 multiplexes neighbor discovery (NS/NA),
// router advertisement (RA) and MLD over the same raw ICMPv6 protocol
// number as echo/error traffic, so without this every one of those L2
// control packets would otherwise reach userspace and wake the receiver
// goroutine for a message it immediately discards. Mirrors Apple's ping6.c
// (ICMP6_FILTER_SETBLOCKALL + SETPASS on ECHO_REPLY only), except mping
// also passes the ICMP error types receiverV6Config needs for its error
// column, traceroute, and MTR.
//
// Non-fatal: ICMP6_FILTER is unsupported on some platforms (and by
// definition on any non-syscall.Conn, such as the fakes used in tests),
// matching the existing best-effort `_ = conn.SetControlMessage(...)` calls
// alongside this one.
func applyICMPv6Filter(conn icmpv6FilterSetter) {
	filt := icmpv6FilterFromConfig(receiverV6Config)
	_ = conn.SetICMPFilter(&filt)
}

func (p *Pinger) runReceiverV4() {
	p.runReceiver(receiverV4Config, func(buf []byte) (int, int, int, net.Addr, error) {
		n, cm, src, err := p.connV4.ReadFrom(buf)
		ttl := 0
		if cm != nil {
			ttl = cm.TTL
		}
		// dscp is always 0 for IPv4: x/net's ipv4.ControlMessage has no TOS
		// field, so there is no receive-side observation available here
		// (see PacketConnV4.SetTOS's doc) — only IPv6 replies ever carry a
		// real Reply.DSCP.
		return n, ttl, 0, src, err
	}, func(t time.Time) error {
		return p.connV4.SetReadDeadline(t)
	}, func(buf []byte, n int, msg *icmp.Message) *icmp.Message {
		// Try parsing as IP packet with header
		if msg == nil && n > 0 && buf[0] == 0x45 {
			ihl := int(buf[0]&0x0f) * 4
			if n > ihl {
				if msg2, err2 := icmp.ParseMessage(1, buf[ihl:n]); err2 == nil {
					return msg2
				}
			}
		}
		return msg
	})
}

func (p *Pinger) runReceiverV6() {
	p.runReceiver(receiverV6Config, func(buf []byte) (int, int, int, net.Addr, error) {
		n, cm, src, err := p.connV6.ReadFrom(buf)
		hopLimit, dscp := 0, 0
		if cm != nil {
			hopLimit = cm.HopLimit
			dscp = cm.TrafficClass
		}
		return n, hopLimit, dscp, src, err
	}, func(t time.Time) error {
		return p.connV6.SetReadDeadline(t)
	}, nil)
}

// runReceiver is the unified receiver loop for both IPv4 and IPv6.
// readFrom returns (bytesRead, ttlOrHopLimit, dscp, srcAddr, error).
// fallbackParse is an optional fallback parser for raw IP packets (used by IPv4).
func (p *Pinger) runReceiver(
	cfg receiverConfig,
	readFrom func(buf []byte) (int, int, int, net.Addr, error),
	setDeadline func(time.Time) error,
	fallbackParse func(buf []byte, n int, msg *icmp.Message) *icmp.Message,
) {
	buf := make([]byte, receiverBufferSize)
	for {
		select {
		case <-p.done:
			return
		default:
			if err := setDeadline(time.Now().Add(receiverReadTimeout)); err != nil {
				return
			}
			n, ttl, dscp, src, err := readFrom(buf)
			if err != nil {
				var opErr *net.OpError
				if errors.As(err, &opErr) && opErr.Timeout() {
					continue
				}
				return
			}

			msg, err := icmp.ParseMessage(cfg.protocol, buf[:n])
			if err != nil {
				if fallbackParse != nil {
					msg = fallbackParse(buf, n, nil)
				}
				if msg == nil {
					continue
				}
			}

			p.broadcastTrace(msg, src)

			if msg.Type == cfg.echoReply {
				p.handleEchoReply(msg, ttl, dscp)
			} else if isErrorType(msg.Type, cfg.errorTypes) {
				p.handleICMPError(msg, cfg.errorStringFn)
			}
		}
	}
}

func isErrorType(t icmp.Type, errorTypes []icmp.Type) bool {
	for _, et := range errorTypes {
		if t == et {
			return true
		}
	}
	return false
}

func (p *Pinger) handleEchoReply(msg *icmp.Message, ttl, dscp int) {
	echo, ok := msg.Body.(*icmp.Echo)
	if !ok {
		return
	}
	p.mapMu.RLock()
	ch, exists := p.targetChans[echo.ID]
	p.mapMu.RUnlock()
	if exists {
		reply := Reply{TTL: ttl, DSCP: dscp, Seq: echo.Seq}
		if rtt, ok := extractSendTimestamp(echo.Data, p.now(), p.probeTimeout); ok {
			reply.RTT = rtt
		}
		select {
		case ch <- reply:
		default:
		}
	}
}

func (p *Pinger) handleICMPError(msg *icmp.Message, errorStringFn func(icmp.Type, int) string) {
	id, seq, ok := extractEchoIDSeq(msg)
	if !ok {
		return
	}
	errMsg := errorStringFn(msg.Type, msg.Code)
	// Packet Too Big's useful diagnostic - the next-hop MTU - lives in the
	// message body, not the type/code pair errorStringFn works from, so it is
	// layered in here once the full *icmp.Message is available.
	if ptb, ok := msg.Body.(*icmp.PacketTooBig); ok {
		errMsg = packetTooBigString(ptb.MTU)
	}
	p.mapMu.RLock()
	ch, exists := p.targetChans[id]
	p.mapMu.RUnlock()
	if exists {
		select {
		case ch <- Reply{Seq: seq, Err: errMsg}:
		default:
		}
	}
}

// resolveIPAddrBounded wraps p.resolveIPAddr with resolveTimeout and aborts
// early when Stop() closes p.done. Mirrors lookupTXTBounded: the resolver
// goroutine is left to finish into a buffered channel (resolveIPAddrFunc
// has no cancellation seam) while the caller is released immediately, so a
// hung resolver cannot stall Pinger.Wait() and with it the whole shutdown.
func (p *Pinger) resolveIPAddrBounded(network, address string) (*net.IPAddr, error) {
	type result struct {
		addr *net.IPAddr
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		addr, err := p.resolveIPAddr(network, address)
		ch <- result{addr, err}
	}()
	select {
	case r := <-ch:
		return r.addr, r.err
	case <-p.done:
		return nil, errPingerStopped
	case <-time.After(resolveTimeout):
		return nil, fmt.Errorf("dns resolution for %q timed out after %s", address, resolveTimeout)
	}
}

// resolveTarget attempts DNS resolution and updates the target's IP.
// Returns the resolved address, or nil if resolution failed or the pinger
// was stopped mid-resolution.
func (p *Pinger) resolveTarget(t *stats.TargetStats) *net.IPAddr {
	addr, err := p.resolveIPAddrBounded("ip", t.Host)
	if err != nil {
		// A stop is not a ping failure: recording one here would inflate
		// the loss count printed by printExitSummary on the way out.
		//
		// Everything else collapses to a generic "DNS Error" on purpose:
		// LastError is surfaced in a narrow TUI column, so the detail
		// resolveIPAddrBounded formats (which host, which timeout) is
		// deliberately dropped here rather than truncated on screen.
		if !errors.Is(err, errPingerStopped) {
			t.OnFailure("DNS Error")
		}
		return nil
	}
	if addr == nil {
		t.OnFailure("DNS Error")
		return nil
	}
	ipStr := addr.String()
	t.SetIP(ipStr)
	if p.AsnEnabled {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.lookupASN(t, ipStr)
		}()
	}
	if p.PtrEnabled {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.lookupPTR(t, ipStr)
		}()
	}
	return addr
}

// ASNInfo holds the full result of a Team Cymru ASN lookup.
type ASNInfo struct {
	Number  string // "AS15169"
	Country string // "US"
	Org     string // "Google LLC"
}

// lookupASN performs a (potentially slow) Team Cymru DNS lookup for ipStr
// and records the result on t. Guarded by p.done so that a lookup queued
// just before Stop() doesn't fire at all. Once a lookup is in flight,
// getASNInfo's calls to lookupTXTBounded also race against p.done: if
// Stop() closes it mid-lookup, getASNInfo returns immediately instead of
// waiting out the underlying DNS query, so Wait() (which blocks on the
// wg.Add/Done in resolveTarget's caller) no longer stalls for
// asnLookupTimeout. The abandoned DNS goroutine is left to finish on its
// own into a buffered channel; only its result is discarded.
func (p *Pinger) lookupASN(t *stats.TargetStats, ipStr string) {
	select {
	case <-p.done:
		return
	default:
	}
	info := p.getASNInfo(ipStr)
	if info.Number != "" {
		t.SetASNInfo(info.Number, info.Country, info.Org)
	}
}

func (p *Pinger) getASN(ipStr string) string {
	return p.getASNInfo(ipStr).Number
}

func (p *Pinger) getASNInfo(ipStr string) ASNInfo {
	p.asnMu.RLock()
	info, found := p.asnCache[ipStr]
	p.asnMu.RUnlock()
	if found {
		return info
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ASNInfo{}
	}

	if j := p.asnJitter(); j > 0 {
		select {
		case <-time.After(j):
		case <-p.done:
			return ASNInfo{}
		}
	}

	var originQuery string
	if ip4 := ip.To4(); ip4 != nil {
		originQuery = fmt.Sprintf("%d.%d.%d.%d.origin.asn.cymru.com", ip4[3], ip4[2], ip4[1], ip4[0])
	} else {
		var sb strings.Builder
		for i := 15; i >= 0; i-- {
			sb.WriteString(fmt.Sprintf("%x.%x.", ip[i]&0xf, ip[i]>>4))
		}
		sb.WriteString("origin6.asn.cymru.com")
		originQuery = sb.String()
	}

	txts, err := p.lookupTXTBounded(originQuery)
	if err != nil || len(txts) == 0 {
		return ASNInfo{}
	}

	// Response example: "15169 | 8.8.8.0/24 | US | arin | 1992-12-01"
	parts := strings.Split(txts[0], "|")
	if len(parts) == 0 {
		return ASNInfo{}
	}

	asnRaw := strings.TrimSpace(parts[0])
	if asnRaw == "NA" || asnRaw == "" {
		return ASNInfo{}
	}
	if !strings.HasPrefix(asnRaw, "AS") {
		asnRaw = "AS" + asnRaw
	}

	var country string
	if len(parts) >= 3 {
		country = strings.TrimSpace(parts[2])
	}

	// Second lookup: <asn-number>.asn.cymru.com for org name
	// Response: "15169 | GOOGLE - Google LLC, US | 1992-12-01"
	org, err := p.lookupOrg(strings.TrimPrefix(asnRaw, "AS"))
	if errors.Is(err, errPingerStopped) {
		// Stop() fired mid-lookup: don't cache a partial record (real ASN/
		// country with a bogus empty Org that's indistinguishable from a
		// genuine "no org found" result).
		return ASNInfo{}
	}

	info = ASNInfo{Number: asnRaw, Country: country, Org: org}
	p.asnMu.Lock()
	p.asnCache[ipStr] = info
	p.asnMu.Unlock()
	return info
}

// lookupTXTBounded wraps p.lookupTXT with asnLookupTimeout and aborts early
// when Stop() closes p.done. The lookup goroutine is left to finish into a
// buffered channel rather than being interrupted — net.LookupTXT has no
// cancellation seam — but the caller is released immediately so Wait()
// doesn't stall the shutdown path.
func (p *Pinger) lookupTXTBounded(name string) ([]string, error) {
	type result struct {
		txts []string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		txts, err := p.lookupTXT(name)
		ch <- result{txts, err}
	}()
	select {
	case r := <-ch:
		return r.txts, r.err
	case <-p.done:
		return nil, errPingerStopped
	case <-time.After(asnLookupTimeout):
		return nil, fmt.Errorf("asn lookup for %q timed out after %s", name, asnLookupTimeout)
	}
}

// lookupOrg resolves the org name for asnNumber. The returned error is
// non-nil only when the lookup aborted because Stop() closed p.done
// (errPingerStopped); a genuine DNS failure or empty/malformed response is
// reported as ("", nil) so callers keep treating it as "no org found", not
// as a reason to discard an otherwise-successful ASN/country result.
func (p *Pinger) lookupOrg(asnNumber string) (string, error) {
	txts, err := p.lookupTXTBounded(asnNumber + ".asn.cymru.com")
	if errors.Is(err, errPingerStopped) {
		return "", err
	}
	if err != nil || len(txts) == 0 {
		return "", nil
	}
	// Response: "15169 | GOOGLE - Google LLC, US | 1992-12-01"
	parts := strings.Split(txts[0], "|")
	if len(parts) < 2 {
		return "", nil
	}
	desc := strings.TrimSpace(parts[1]) // "GOOGLE - Google LLC, US"
	// Strip "HANDLE - " prefix if present
	if idx := strings.Index(desc, " - "); idx >= 0 {
		desc = strings.TrimSpace(desc[idx+3:]) // "Google LLC, US"
	}
	// Strip trailing ", CC" country suffix
	if idx := strings.LastIndex(desc, ", "); idx >= 0 {
		desc = strings.TrimSpace(desc[:idx]) // "Google LLC"
	}
	return desc, nil
}

// dscpFor returns the outbound DSCP override for t, if any, sourced from
// Pinger.TargetDSCP (keyed by t.Host — see the field's doc for why per-
// target overrides can't just use SetTOS/SetTrafficClass). ok is false when
// t has no override, in which case the caller should fall back to the
// socket-wide default Start() already armed via DSCP.
func (p *Pinger) dscpFor(t *stats.TargetStats) (int, bool) {
	if p.TargetDSCP == nil {
		return 0, false
	}
	v, ok := p.TargetDSCP[t.Host]
	return v, ok
}

// getWriteFunc returns the appropriate ICMP message type and write function
// for the given destination address. dscp, when ok, is attached as an
// explicit per-packet ipv4.ControlMessage.TOS / ipv6.ControlMessage.
// TrafficClass on every WriteTo the returned func makes — overriding, for
// this target only, the socket-wide default Start() set via SetTOS/
// SetTrafficClass (or the OS default when that wasn't set either).
func (p *Pinger) getWriteFunc(dstAddr *net.IPAddr, dscp int, ok bool) (icmp.Type, func([]byte, net.Addr) (int, error), string) {
	isV4 := dstAddr.IP.To4() != nil
	if isV4 {
		if p.connV4 != nil {
			// dscp/ok are intentionally unused here: x/net's
			// ipv4.ControlMessage has no TOS field, so IPv4 has no
			// per-packet write-side hook to attach a per-target override
			// to (see PacketConnV4.SetTOS's doc) — every IPv4 target gets
			// only the socket-wide default Start() armed via SetTOS.
			return ipv4.ICMPTypeEcho, func(b []byte, dst net.Addr) (int, error) {
				return p.connV4.WriteTo(b, nil, dst)
			}, ""
		}
		return nil, nil, "No IPv4 Conn"
	}
	if p.connV6 != nil {
		var cm *ipv6.ControlMessage
		if ok {
			cm = &ipv6.ControlMessage{TrafficClass: dscp}
		}
		return ipv6.ICMPTypeEchoRequest, func(b []byte, dst net.Addr) (int, error) {
			return p.connV6.WriteTo(b, cm, dst)
		}, ""
	}
	return nil, nil, "No IPv6 Conn"
}

// sendProbe marshals and sends an ICMP echo request. Returns the send time, or an error string.
func (p *Pinger) sendProbe(t *stats.TargetStats, id, seq int, payload []byte, dstAddr *net.IPAddr) (time.Time, bool) {
	dscp, dscpOK := p.dscpFor(t)
	msgType, writeFunc, errStr := p.getWriteFunc(dstAddr, dscp, dscpOK)
	if writeFunc == nil {
		t.OnFailure(errStr)
		return time.Time{}, false
	}

	// start is captured before the payload is marshaled (rather than right
	// before the write, as before) because embedSendTimestamp needs it
	// embedded in `payload` first. This is the same instant used for both
	// the embedded timestamp and the pendingProbe fallback in runWorker, so
	// the two stay consistent with each other.
	start := time.Now()
	embedSendTimestamp(payload, start)

	msg := icmp.Message{
		Type: msgType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  seq & seqMask,
			Data: payload,
		},
	}
	b, err := marshalProbe(&msg)
	if err != nil {
		return time.Time{}, false
	}

	_, err = writeFunc(b, dstAddr)
	if err != nil {
		errMsg := p.applyLastErrSource(err.Error())
		t.OnFailure(errMsg)
		p.log(t, seq, "SendError", 0, 0, err.Error())
		return time.Time{}, false
	}

	t.IncSent()
	return start, true
}

// pendingProbe tracks one in-flight probe: the logical (unbounded) seq used
// for CSV logging, and the time it was sent (for RTT and expiry).
type pendingProbe struct {
	logicalSeq int
	start      time.Time
}

// nextExpiry returns the earliest deadline among unacked's entries, and
// whether unacked is non-empty. Because runWorker only ever inserts entries
// with monotonically increasing start times and a single, constant timeout,
// the earliest deadline is a plain O(n) scan; n is bounded by the number of
// probes currently in flight (interval/timeout ratio), which stays small in
// practice.
func nextExpiry(unacked map[int]pendingProbe, timeout time.Duration) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, pend := range unacked {
		d := pend.start.Add(timeout)
		if !found || d.Before(earliest) {
			earliest, found = d, true
		}
	}
	return earliest, found
}

// rearmSweepTimer stops/drains sweepTimer and reschedules it for the
// earliest still-outstanding deadline in unacked, or leaves it stopped when
// unacked is empty. Only ever called from runWorker's own goroutine, so no
// synchronization is needed around the Stop/drain/Reset sequence.
func rearmSweepTimer(sweepTimer *time.Timer, unacked map[int]pendingProbe, timeout time.Duration) {
	if !sweepTimer.Stop() {
		select {
		case <-sweepTimer.C:
		default:
		}
	}
	if d, ok := nextExpiry(unacked, timeout); ok {
		wait := time.Until(d)
		if wait < 0 {
			wait = 0
		}
		sweepTimer.Reset(wait)
	}
}

// runWorker drives one target's probe traffic with a single select loop
// that decouples sending from waiting on replies (see Apple ping.c's
// select()-based main loop, which never blocks sending on a reply):
//
//   - sendTimer fires immediately for the first probe (matching the prior
//     stop-and-wait runWorker, which never gated its first send on a
//     tick), then rearms itself for `interval` after every subsequent
//     fire. Unlike a stop-and-wait design, later sends are never gated on
//     a prior probe's reply, so a target that never answers is still
//     probed once per `interval`, not once per `timeout`.
//   - ch delivers replies from the receiver goroutine; a reply is matched
//     against `unacked` by its (16-bit-masked) seq and, if found, recorded
//     as a success or ICMP error and removed. A reply with no matching
//     entry — a duplicate of an already-resolved seq, or one that arrived
//     after its entry was already swept as a timeout — is silently
//     discarded, exactly as the prior stop-and-wait waitForReply did for
//     any non-matching reply.
//   - sweepTimer fires at the earliest deadline among `unacked` and
//     records every entry that has aged past `timeout` as a loss.
//   - dnsTicker re-resolves the target on the same cadence as before.
//   - p.done ends the loop immediately, matching Stop()/Close().
//
// `unacked` is owned exclusively by this goroutine, so it needs no mutex.
//
// Count still means "stop after sending Count probes", but since probes can
// now be in flight concurrently, reaching Count no longer implies the most
// recent probe (or any earlier one) has already been resolved: once the
// Count-th probe is sent, the loop stops sending but keeps servicing
// replies/sweeps — draining `unacked` — until every outstanding probe has
// been resolved, then returns. This is the same "wait out the stragglers
// before reporting the final tally" idea as ping.c's almost_done.
func (p *Pinger) runWorker(t *stats.TargetStats, id int, interval, timeout time.Duration) {
	dstAddr := p.resolveTarget(t)

	seq := p.initialSeq
	// sendTimer fires once immediately (duration 0) for the first probe,
	// then is explicitly Reset(interval) after every fire that doesn't
	// terminate sending — see the case below. This reproduces the prior
	// implementation's "send right away, then pace off the ticker"
	// structure without a separate pre-loop send.
	sendTimer := time.NewTimer(0)
	defer sendTimer.Stop()

	resInterval := p.ResolveInterval
	if resInterval <= 0 {
		resInterval = 60 * time.Second
	}
	dnsTicker := time.NewTicker(resInterval)
	defer dnsTicker.Stop()

	// sweepTimer is created stopped: it's only armed once the first probe
	// is sent (rearmSweepTimer no-ops while unacked is empty).
	sweepTimer := time.NewTimer(timeout)
	if !sweepTimer.Stop() {
		<-sweepTimer.C
	}
	defer sweepTimer.Stop()

	p.mapMu.RLock()
	ch := p.targetChans[id]
	p.mapMu.RUnlock()

	payload := buildPayload(p.Size)

	unacked := make(map[int]pendingProbe)
	doneSending := false // set once seq reaches p.Count (Count<=0 means unlimited)

	// replyCh stays nil (permanently blocking that select case) until the
	// first probe is actually written to the wire. A real ICMP reply can
	// never arrive before its request was sent, so this changes nothing
	// observable in production — but it matters for the select loop
	// itself: without this gate, the loop would be a ready reader on ch
	// from goroutine start, before `unacked` has its first entry, so any
	// reply delivered in that window would find no match and be discarded
	// as if it were a stale duplicate. Gating the case off until a send has
	// actually happened preserves the intuitive invariant that a reply is
	// only ever evaluated against an `unacked` that could possibly contain
	// it.
	var replyCh <-chan Reply

	for {
		select {
		case <-p.done:
			return

		case <-dnsTicker.C:
			if newAddr := p.resolveTarget(t); newAddr != nil {
				dstAddr = newAddr
			}

		case <-sendTimer.C:
			if doneSending {
				continue
			}
			if dstAddr == nil {
				if addr := p.resolveTarget(t); addr != nil {
					dstAddr = addr
				} else {
					sendTimer.Reset(interval) // retry resolution on the next tick, as before
					continue
				}
			}

			seq++

			start, ok := p.sendProbe(t, id, seq, payload, dstAddr)
			if ok {
				unacked[seq&seqMask] = pendingProbe{logicalSeq: seq, start: start}
				rearmSweepTimer(sweepTimer, unacked, timeout)
				replyCh = ch
			}

			if p.Count > 0 && seq >= p.Count {
				doneSending = true
			} else {
				sendTimer.Reset(interval)
			}

		case reply := <-replyCh:
			// A reply with no matching entry is a duplicate of an
			// already-resolved seq, or a late arrival for a seq already
			// swept as a timeout: discard it silently, matching the prior
			// waitForReply's behavior for any non-matching reply.
			if pend, found := unacked[reply.Seq]; found {
				delete(unacked, reply.Seq)
				if reply.Err != "" {
					t.OnFailure(reply.Err)
					p.log(t, pend.logicalSeq, "ICMPError", 0, 0, reply.Err)
				} else {
					// Prefer the RTT the receiver goroutine computed from the
					// payload-embedded send timestamp (see handleEchoReply):
					// it skips this goroutine hop entirely, so it isn't
					// inflated by scheduling delay between the receiver and
					// this select loop. reply.RTT is left at its zero value
					// whenever that timestamp couldn't be trusted (payload
					// too small for -s, signature mismatch, or a result
					// isPlausibleRTT rejected), in which case fall back to
					// this goroutine's own start-time bookkeeping, exactly
					// as before this feature existed.
					rtt := reply.RTT
					if rtt <= 0 {
						rtt = time.Since(pend.start)
					}
					t.OnSuccess(rtt, reply.TTL)
					t.SetLastDSCP(reply.DSCP)
					p.log(t, pend.logicalSeq, "OK", rtt, reply.TTL, "")
				}
				rearmSweepTimer(sweepTimer, unacked, timeout)
			}

		case <-sweepTimer.C:
			now := time.Now()
			for wireSeq, pend := range unacked {
				if now.Sub(pend.start) >= timeout {
					delete(unacked, wireSeq)
					errMsg := p.applyLastErrSource("Timeout")
					t.OnFailure(errMsg)
					p.log(t, pend.logicalSeq, "Timeout", 0, 0, "Request timed out")
				}
			}
			rearmSweepTimer(sweepTimer, unacked, timeout)
		}

		if doneSending && len(unacked) == 0 {
			return
		}
	}
}

func buildPayload(size int) []byte {
	if size < 0 {
		size = 0
	}
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = 'A' // Fill with pattern
	}
	// Embed signature at the beginning if size permits
	if len(payload) >= len(payloadSignature) {
		copy(payload, payloadSignature)
	}
	return payload
}

// marshalProbe serializes an ICMP echo request for transmission.
//
// psh (the pseudo header argument to icmp.Message.Marshal) must stay nil
// here. For ICMPv4 it's ignored entirely, but for ICMPv6, x/net treats a
// non-nil psh as real pseudo-header bytes: it writes a 4-byte message
// length field at a fixed offset (2*net.IPv6len = 32) into the buffer
// and computes a checksum over it. A non-nil-but-empty slice (as this
// package used to pass via a pooled buffer for size<=1400) satisfies
// "non-nil" without being an actual pseudo header, so that length write
// lands inside the ICMP payload once it's long enough to reach offset
// 32, silently corrupting it. Passing nil defers checksum computation to
// the kernel, which is required anyway since a raw ICMPv6 socket always
// recomputes and overwrites the checksum on send.
func marshalProbe(msg *icmp.Message) ([]byte, error) {
	return msg.Marshal(nil)
}
