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

	asnJitterMax = 2 * time.Second // spreads concurrent targets' ASN lookups to avoid bursting Cymru's public server
)

// asnLookupTimeout bounds each Cymru DNS TXT query, since neither net.LookupTXT
// nor a *net.Resolver wrapped with context.Background() has a timeout of its
// own. A var (not const) so tests can shrink it instead of waiting 3s.
var asnLookupTimeout = 3 * time.Second

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
}

// PacketConnV6 interface matches *ipv6.PacketConn methods we use
type PacketConnV6 interface {
	ReadFrom(b []byte) (int, *ipv6.ControlMessage, net.Addr, error)
	WriteTo(b []byte, cm *ipv6.ControlMessage, dst net.Addr) (int, error)
	SetReadDeadline(t time.Time) error
	Close() error
	SetControlMessage(cf ipv6.ControlFlags, on bool) error
}

// Reply represents a single received ICMP echo reply or error from the receiver loop.
type Reply struct {
	RTT time.Duration
	TTL int
	Seq int
	Err string
}

type traceMsg struct {
	parsed *icmp.Message
	src    net.Addr
}

type Pinger struct {
	Targets []*stats.TargetStats

	Source string // Source IP address to bind to
	Size   int    // Payload size in bytes
	Count  int    // Stop after sending Count packets (0 = infinite)

	ResolveInterval time.Duration // Interval to re-resolve DNS
	AsnEnabled      bool          // Enable ASN lookups

	connV4      PacketConnV4
	connV6      PacketConnV6
	targetMap   map[int]*stats.TargetStats
	targetChans map[int]chan Reply
	mapMu       sync.RWMutex
	baseID      int

	// initialSeq is the value runWorker's logical seq counter starts from
	// (before the first seq++). Always 0 in production; tests override it
	// via Options.InitialSeq to reach the 16-bit wraparound boundary
	// without waiting for 65535 real probes.
	initialSeq int

	asnCache  map[string]ASNInfo
	asnMu     sync.RWMutex
	asnJitter func() time.Duration // returns a random delay to stagger concurrent Cymru lookups; overridden to 0 in tests

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
}

type resolveIPAddrFunc func(network, address string) (*net.IPAddr, error)

type listenPacketFunc func(network, address string) (net.PacketConn, error)

type Options struct {
	ResolveIPAddr resolveIPAddrFunc
	Resolver      *net.Resolver
	Now           func() time.Time
	ListenPacket  listenPacketFunc
	LookupTXT     func(string) ([]string, error)
	AsnEnabled    bool

	// InitialSeq overrides the starting value of runWorker's logical seq
	// counter. Zero value matches production behavior (start at 0); tests
	// set it to exercise the 16-bit ICMP seq wraparound boundary.
	InitialSeq int
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

	return &Pinger{
		Targets:         targets,
		targetMap:       make(map[int]*stats.TargetStats),
		targetChans:     make(map[int]chan Reply),
		asnCache:        make(map[string]ASNInfo),
		baseID:          os.Getpid() & 0xffff,
		Size:            56, // Default payload size (like standard ping)
		ResolveInterval: 60 * time.Second,
		AsnEnabled:      opts.AsnEnabled,
		traceChans:      make(map[int]chan traceMsg),
		done:            make(chan struct{}),
		initialSeq:      opts.InitialSeq,
		resolveIPAddr:   resolve,
		now:             now,
		listenPacket:    listen,
		lookupTXT:       lookup,
		asnJitter:       func() time.Duration { return time.Duration(rand.Int63n(int64(asnJitterMax))) },
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
			p.connV6 = ipv6.NewPacketConn(c)
			// Non-fatal: hop limit control message may not be available on all platforms.
			_ = p.connV6.SetControlMessage(ipv6.FlagHopLimit, true)
		} else {
			errV6 = err
		}
	}

	if p.connV4 == nil && p.connV6 == nil {
		return fmt.Errorf("failed to initialize pinger: v4=%v, v6=%v", errV4, errV6)
	}

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

func (p *Pinger) runReceiverV4() {
	p.runReceiver(receiverV4Config, func(buf []byte) (int, int, net.Addr, error) {
		n, cm, src, err := p.connV4.ReadFrom(buf)
		ttl := 0
		if cm != nil {
			ttl = cm.TTL
		}
		return n, ttl, src, err
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
	p.runReceiver(receiverV6Config, func(buf []byte) (int, int, net.Addr, error) {
		n, cm, src, err := p.connV6.ReadFrom(buf)
		hopLimit := 0
		if cm != nil {
			hopLimit = cm.HopLimit
		}
		return n, hopLimit, src, err
	}, func(t time.Time) error {
		return p.connV6.SetReadDeadline(t)
	}, nil)
}

// runReceiver is the unified receiver loop for both IPv4 and IPv6.
// readFrom returns (bytesRead, ttlOrHopLimit, srcAddr, error).
// fallbackParse is an optional fallback parser for raw IP packets (used by IPv4).
func (p *Pinger) runReceiver(
	cfg receiverConfig,
	readFrom func(buf []byte) (int, int, net.Addr, error),
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
			n, ttl, src, err := readFrom(buf)
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
				p.handleEchoReply(msg, ttl)
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

func (p *Pinger) handleEchoReply(msg *icmp.Message, ttl int) {
	echo, ok := msg.Body.(*icmp.Echo)
	if !ok {
		return
	}
	p.mapMu.RLock()
	ch, exists := p.targetChans[echo.ID]
	p.mapMu.RUnlock()
	if exists {
		select {
		case ch <- Reply{TTL: ttl, Seq: echo.Seq}:
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

// getWriteFunc returns the appropriate ICMP message type and write function
// for the given destination address.
func (p *Pinger) getWriteFunc(dstAddr *net.IPAddr) (icmp.Type, func([]byte, net.Addr) (int, error), string) {
	isV4 := dstAddr.IP.To4() != nil
	if isV4 {
		if p.connV4 != nil {
			return ipv4.ICMPTypeEcho, func(b []byte, dst net.Addr) (int, error) {
				return p.connV4.WriteTo(b, nil, dst)
			}, ""
		}
		return nil, nil, "No IPv4 Conn"
	}
	if p.connV6 != nil {
		return ipv6.ICMPTypeEchoRequest, func(b []byte, dst net.Addr) (int, error) {
			return p.connV6.WriteTo(b, nil, dst)
		}, ""
	}
	return nil, nil, "No IPv6 Conn"
}

// sendProbe marshals and sends an ICMP echo request. Returns the send time, or an error string.
func (p *Pinger) sendProbe(t *stats.TargetStats, id, seq int, payload []byte, dstAddr *net.IPAddr) (time.Time, bool) {
	msgType, writeFunc, errStr := p.getWriteFunc(dstAddr)
	if writeFunc == nil {
		t.OnFailure(errStr)
		return time.Time{}, false
	}

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

	start := time.Now()
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

// waitForReply waits for a matching reply or timeout. Returns false if done channel was closed.
func (p *Pinger) waitForReply(t *stats.TargetStats, ch <-chan Reply, seq int, start time.Time, timeoutTimer *time.Timer) bool {
	for {
		select {
		case reply := <-ch:
			if reply.Seq != seq&seqMask {
				continue
			}
			if reply.Err != "" {
				t.OnFailure(reply.Err)
				p.log(t, seq, "ICMPError", 0, 0, reply.Err)
			} else {
				rtt := time.Since(start)
				t.OnSuccess(rtt, reply.TTL)
				p.log(t, seq, "OK", rtt, reply.TTL, "")
			}
			timeoutTimer.Stop()
			return true
		case <-timeoutTimer.C:
			errMsg := p.applyLastErrSource("Timeout")
			t.OnFailure(errMsg)
			p.log(t, seq, "Timeout", 0, 0, "Request timed out")
			return true
		case <-p.done:
			timeoutTimer.Stop()
			return false
		}
	}
}

func (p *Pinger) runWorker(t *stats.TargetStats, id int, interval, timeout time.Duration) {
	dstAddr := p.resolveTarget(t)

	seq := p.initialSeq
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	resInterval := p.ResolveInterval
	if resInterval <= 0 {
		resInterval = 60 * time.Second
	}
	dnsTicker := time.NewTicker(resInterval)
	defer dnsTicker.Stop()

	var timeoutTimer *time.Timer

	p.mapMu.RLock()
	ch := p.targetChans[id]
	p.mapMu.RUnlock()

	payload := buildPayload(p.Size)

	for {
		if p.Count > 0 && seq >= p.Count {
			return
		}

		select {
		case <-dnsTicker.C:
			if newAddr := p.resolveTarget(t); newAddr != nil {
				dstAddr = newAddr
			}
		default:
		}

		if dstAddr == nil {
			if addr := p.resolveTarget(t); addr != nil {
				dstAddr = addr
			} else {
				select {
				case <-p.done:
					return
				case <-ticker.C:
					continue
				}
			}
		}

		seq++

		start, ok := p.sendProbe(t, id, seq, payload, dstAddr)
		if !ok {
			select {
			case <-p.done:
				return
			case <-ticker.C:
				continue
			}
		}

		if timeoutTimer == nil {
			timeoutTimer = time.NewTimer(timeout)
		} else {
			if !timeoutTimer.Stop() {
				select {
				case <-timeoutTimer.C:
				default:
				}
			}
			timeoutTimer.Reset(timeout)
		}

		if !p.waitForReply(t, ch, seq, start, timeoutTimer) {
			return
		}

		if p.Count > 0 && seq >= p.Count {
			return
		}

		select {
		case <-p.done:
			return
		case <-ticker.C:
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
