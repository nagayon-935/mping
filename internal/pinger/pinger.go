package pinger

import (
	"errors"
	"fmt"
	"io"
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
)

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

	asnCache map[string]ASNInfo
	asnMu    sync.RWMutex

	traceChans   []chan traceMsg // one per concurrent TraceRoute call
	traceChansMu sync.RWMutex
	traceCounter atomic.Uint32 // unique traceID per concurrent call

	LogWriter io.Writer // Optional logger

	done chan struct{} // Signal to close receiver
	wg   sync.WaitGroup

	resolveIPAddr resolveIPAddrFunc
	now           func() time.Time
	listenPacket  listenPacketFunc
	lookupTXT     func(string) ([]string, error)
}

type resolveIPAddrFunc func(network, address string) (*net.IPAddr, error)

type listenPacketFunc func(network, address string) (net.PacketConn, error)

type Options struct {
	ResolveIPAddr resolveIPAddrFunc
	Now           func() time.Time
	ListenPacket  listenPacketFunc
	LookupTXT     func(string) ([]string, error)
	AsnEnabled    bool
}

// NewPinger creates a Pinger with default options for the given targets.
func NewPinger(targets []*stats.TargetStats) *Pinger {
	return NewPingerWithOptions(targets, Options{})
}

// NewPingerWithOptions creates a Pinger with the provided options.
func NewPingerWithOptions(targets []*stats.TargetStats, opts Options) *Pinger {
	resolve := opts.ResolveIPAddr
	if resolve == nil {
		resolve = net.ResolveIPAddr
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
		lookup = net.LookupTXT
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
		done:            make(chan struct{}),
		resolveIPAddr:   resolve,
		now:             now,
		listenPacket:    listen,
		lookupTXT:       lookup,
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

func (p *Pinger) applyLastErrSource(errMsg string) string {
	if p.Source != "" && strings.Contains(errMsg, "write ip 0.0.0.0->") {
		return strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+p.Source+"->", 1)
	}
	return errMsg
}

func (p *Pinger) Stop() {
	if p.done != nil {
		select {
		case <-p.done:
			// Already closed
		default:
			close(p.done)
		}
	}
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
		go p.runReceiverV4()
	}
	if p.connV6 != nil {
		go p.runReceiverV6()
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

func (p *Pinger) broadcastTrace(msg *icmp.Message, src net.Addr) {
	p.traceChansMu.RLock()
	for _, ch := range p.traceChans {
		select {
		case ch <- traceMsg{msg, src}:
		default:
		}
	}
	p.traceChansMu.RUnlock()
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
	protocol:      58,
	echoReply:     ipv6.ICMPTypeEchoReply,
	errorTypes:    []icmp.Type{ipv6.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeTimeExceeded, ipv6.ICMPTypeParameterProblem},
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
			setDeadline(time.Now().Add(receiverReadTimeout))
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

// resolveTarget attempts DNS resolution and updates the target's IP.
// Returns the resolved address, or nil if resolution failed.
func (p *Pinger) resolveTarget(t *stats.TargetStats) *net.IPAddr {
	addr, err := p.resolveIPAddr("ip", t.Host)
	if err != nil {
		t.OnFailure("DNS Error")
		return nil
	}
	ipStr := addr.String()
	t.SetIP(ipStr)
	if p.AsnEnabled {
		go p.lookupASN(t, ipStr)
	}
	return addr
}

// ASNInfo holds the full result of a Team Cymru ASN lookup.
type ASNInfo struct {
	Number  string // "AS15169"
	Country string // "US"
	Org     string // "Google LLC"
}

func (p *Pinger) lookupASN(t *stats.TargetStats, ipStr string) {
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

	txts, err := p.lookupTXT(originQuery)
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
	org := p.lookupOrg(strings.TrimPrefix(asnRaw, "AS"))

	info = ASNInfo{Number: asnRaw, Country: country, Org: org}
	p.asnMu.Lock()
	p.asnCache[ipStr] = info
	p.asnMu.Unlock()
	return info
}

func (p *Pinger) lookupOrg(asnNumber string) string {
	txts, err := p.lookupTXT(asnNumber + ".asn.cymru.com")
	if err != nil || len(txts) == 0 {
		return ""
	}
	// Response: "15169 | GOOGLE - Google LLC, US | 1992-12-01"
	parts := strings.Split(txts[0], "|")
	if len(parts) < 2 {
		return ""
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
	return desc
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
			Seq:  seq,
			Data: payload,
		},
	}
	b, err := msg.Marshal(nil)
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
			if reply.Seq != seq {
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

	seq := 0
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
