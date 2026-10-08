package pinger

import (
	"context"
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

	// recentSeqHistoryCap bounds recentSeqHistory, the FIFO runWorker keeps
	// of recently-resolved wire seqs (see recentSeqHistory's doc) so it can
	// classify a reply that no longer matches `unacked` as a duplicate or a
	// late arrival instead of silently discarding it.
	//
	// It must stay far below seqMask+1 (65536, the full 16-bit wraparound
	// period a wire seq value cycles through): once a wire seq value is
	// reused by a brand-new logical probe (a fresh unacked[wireSeq] entry),
	// any history this cache still held from that value's *previous*
	// generation must already have been evicted -- otherwise a reply for
	// the *new* generation could be misclassified against *stale* history.
	// 512 gives a generous multi-thousand-probe margin below one full
	// wraparound while comfortably covering the realistic dup/late window
	// (network duplicates and late arrivals show up within a handful of
	// probes of the original, not thousands later).
	recentSeqHistoryCap = 512

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

// errPingerStopped is returned by the bounded lookup helpers when Stop()
// closed p.done while a call was in flight. Callers use it to distinguish
// "we are shutting down" from a genuine DNS failure, so shutdown doesn't
// record a spurious loss against the target.
var errPingerStopped = errors.New("pinger stopped")

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

	// TargetDSCP holds per-target outbound overrides by stats identity, so two
	// entries for the same host can use different IPv6 traffic classes.
	TargetDSCP map[*stats.TargetStats]int

	connV4           PacketConnV4
	connV6           PacketConnV6
	targetMap        map[int]*stats.TargetStats
	targetChans      map[int]chan Reply
	mapMu            sync.RWMutex
	baseID           int
	ids              *IDAllocator
	dynamicMu        sync.Mutex // Serializes AddTarget/RemoveTarget with Start.
	workerStates     map[*stats.TargetStats]*targetWorker
	workerEvents     chan struct{}
	activeWorkers    int
	nextWorkerID     int
	interval         time.Duration
	resolveAddresses map[*stats.TargetStats]string

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

	ctx      context.Context // lifetime of all context-aware DNS operations
	cancel   context.CancelFunc
	done     chan struct{} // Signal to close receiver
	stopOnce sync.Once     // guards close(done); Stop may be called concurrently
	wg       sync.WaitGroup
	workers  sync.WaitGroup // probe completion excludes the long-lived receivers

	resolveIPAddr      resolveIPAddrFunc
	resolveWithContext func(context.Context, string, string) (*net.IPAddr, error)
	now                func() time.Time
	listenPacket       listenPacketFunc
	lookupTXT          func(string) ([]string, error)
	lookupAddr         func(string) ([]string, error)
	lookupTXTContext   func(context.Context, string) ([]string, error)
	lookupAddrContext  func(context.Context, string) ([]string, error)
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
	IDs *IDAllocator // Shared for the session, including explicit restarts.
	// Context-aware hooks must honor cancellation. Default DNS uses these
	// hooks synchronously on the owning worker/trace. Context-free hooks are
	// retained for compatibility: their bounded adapters may outlive Stop,
	// so injectors must ensure they return. Context-aware hooks take priority.
	ResolveIPAddr        resolveIPAddrFunc
	ResolveIPAddrContext func(context.Context, string, string) (*net.IPAddr, error)
	Resolver             *net.Resolver
	Now                  func() time.Time
	ListenPacket         listenPacketFunc
	LookupTXT            func(string) ([]string, error)
	LookupAddr           func(string) ([]string, error)
	LookupTXTContext     func(context.Context, string) ([]string, error)
	LookupAddrContext    func(context.Context, string) ([]string, error)
	AsnEnabled           bool
	PtrEnabled           bool

	// InitialSeq overrides the starting value of runWorker's logical seq
	// counter. Zero value matches production behavior (start at 0); tests
	// set it to exercise the 16-bit ICMP seq wraparound boundary.
	InitialSeq int

	// DSCP is the global outbound DSCP-derived TOS/TrafficClass byte
	// (0-255). Nil (the zero value) leaves the OS default in place — a
	// *int rather than a plain int so "not configured" is distinguishable
	// from an explicit "0" (CS0/Default) selection.
	DSCP *int

	// TargetDSCP overrides DSCP by index in the targets passed to the constructor.
	TargetDSCP map[int]int
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
	resolveContext := opts.ResolveIPAddrContext
	if resolveContext == nil && resolve == nil {
		resolveContext = func(ctx context.Context, network, address string) (*net.IPAddr, error) {
			return ResolveIPAddrContext(ctx, opts.Resolver, network, address)
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
	resolver := opts.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	lookupContext := opts.LookupTXTContext
	if lookupContext == nil && opts.LookupTXT == nil {
		lookupContext = resolver.LookupTXT
	}
	lookupAddrContext := opts.LookupAddrContext
	if lookupAddrContext == nil && opts.LookupAddr == nil {
		lookupAddrContext = resolver.LookupAddr
	}

	dscp := dscpUnset
	if opts.DSCP != nil {
		dscp = *opts.DSCP
	}

	var targetDSCP map[*stats.TargetStats]int
	if len(opts.TargetDSCP) > 0 {
		targetDSCP = make(map[*stats.TargetStats]int, len(opts.TargetDSCP))
		for i, value := range opts.TargetDSCP {
			if i >= 0 && i < len(targets) {
				targetDSCP[targets[i]] = value
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Pinger{
		Targets:            targets,
		ids:                opts.IDs,
		workerStates:       make(map[*stats.TargetStats]*targetWorker),
		workerEvents:       make(chan struct{}, 1),
		resolveAddresses:   make(map[*stats.TargetStats]string),
		targetMap:          make(map[int]*stats.TargetStats),
		targetChans:        make(map[int]chan Reply),
		asnCache:           make(map[string]ASNInfo),
		ptrCache:           make(map[string]string),
		baseID:             os.Getpid() & 0xffff,
		Size:               56, // Default payload size (like standard ping)
		ResolveInterval:    60 * time.Second,
		AsnEnabled:         opts.AsnEnabled,
		PtrEnabled:         opts.PtrEnabled,
		DSCP:               dscp,
		TargetDSCP:         targetDSCP,
		traceChans:         make(map[int]chan traceMsg),
		ctx:                ctx,
		cancel:             cancel,
		done:               make(chan struct{}),
		initialSeq:         opts.InitialSeq,
		resolveIPAddr:      resolve,
		resolveWithContext: resolveContext,
		now:                now,
		listenPacket:       listen,
		lookupTXT:          opts.LookupTXT,
		lookupAddr:         opts.LookupAddr,
		lookupTXTContext:   lookupContext,
		lookupAddrContext:  lookupAddrContext,
		asnJitter:          func() time.Duration { return time.Duration(rand.Int63n(int64(asnJitterMax))) },
		ptrJitter:          func() time.Duration { return time.Duration(rand.Int63n(int64(ptrJitterMax))) },
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

// Stop cancels the pinger lifetime and signals workers/receivers. The owner
// calls Wait before releasing sockets through Close; Stop is idempotent.
func (p *Pinger) Stop() {
	if p.done == nil {
		return
	}
	p.stopOnce.Do(func() {
		close(p.done)
		if p.cancel != nil {
			p.cancel()
		}
	})
}

func (p *Pinger) Start(interval, timeout time.Duration) error {
	p.probeTimeout = timeout
	p.interval = interval

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

	// Allocate the entire initial set before starting any workers.
	ids := make([]int, len(p.Targets))
	for i := range p.Targets {
		id, err := p.allocateWorkerID()
		if err != nil {
			return err
		}
		ids[i] = id
	}
	for i, t := range p.Targets {
		p.launchTarget(t, ids[i])
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

// WaitWorkers waits for all targets to finish their outstanding probes. Unlike
// Wait, it can return on Count completion while receivers serve MTR/traceroute.
// Call only after Start has returned.
func (p *Pinger) WaitWorkers() {
	p.workers.Wait()
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
