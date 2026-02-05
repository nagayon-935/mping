package pinger

import (
	"errors"
	"fmt"
	"io"
	"github.com/nagayon-935/mping/internal/stats"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
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

// Reply represents a received ICMP echo reply.
type Reply struct {
	RTT time.Duration
	TTL int
	Seq int
	Err string
}

type Pinger struct {
	Targets []*stats.TargetStats

	Source string // Source IP address to bind to
	Size   int    // Payload size in bytes
	Count  int    // Stop after sending Count packets (0 = infinite)
	
	ResolveInterval time.Duration // Interval to re-resolve DNS

	connV4      PacketConnV4
	connV6      PacketConnV6
	targetMap   map[int]*stats.TargetStats
	targetChans map[int]chan Reply
	mapMu       sync.RWMutex
	baseID      int

	LogWriter io.Writer // Optional logger

	done chan struct{} // Signal to close receiver
	wg   sync.WaitGroup

	resolveIPAddr resolveIPAddrFunc
	now           func() time.Time
	listenPacket  listenPacketFunc

	lastErrMu  sync.Mutex
	lastErrMsg string
}

type resolveIPAddrFunc func(network, address string) (*net.IPAddr, error)

type listenPacketFunc func(network, address string) (net.PacketConn, error)

type Options struct {
	ResolveIPAddr resolveIPAddrFunc
	Now           func() time.Time
	ListenPacket  listenPacketFunc
}

var canSendPayloadFn = (*Pinger).canSendPayload

func NewPinger(targets []*stats.TargetStats) *Pinger {
	return NewPingerWithOptions(targets, Options{})
}

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

	return &Pinger{
		Targets:         targets,
		targetMap:       make(map[int]*stats.TargetStats),
		targetChans:     make(map[int]chan Reply),
		baseID:          os.Getpid() & 0xffff,
		Size:            56, // Default payload size (like standard ping)
		ResolveInterval: 60 * time.Second,
		done:            make(chan struct{}),
		resolveIPAddr:   resolve,
		now:             now,
		listenPacket:    listen,
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

	p.LogWriter.Write([]byte(line))
}

func (p *Pinger) setLastErr(err error) {
	if err == nil {
		return
	}
	p.lastErrMu.Lock()
	p.lastErrMsg = err.Error()
	p.lastErrMu.Unlock()
}

func (p *Pinger) applyLastErrSource(errMsg string) string {
	if p.Source != "" && strings.Contains(errMsg, "write ip 0.0.0.0->") {
		return strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+p.Source+"->", 1)
	}
	p.lastErrMu.Lock()
	lastErr := p.lastErrMsg
	p.lastErrMu.Unlock()
	if p.Source != "" && strings.Contains(lastErr, "write ip 0.0.0.0->") && lastErr == errMsg {
		return strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+p.Source+"->", 1)
	}
	return errMsg
}

func (p *Pinger) DiscoverMaxPayload(dest string, start int, min int, privileged bool, logf func(string)) (int, error) {
	if dest == "" {
		return 0, fmt.Errorf("destination is empty")
	}
	if start <= 0 {
		return 0, fmt.Errorf("start MTU must be > 0")
	}
	if min < 0 {
		min = 0
	}

	dstAddr, err := p.resolveIPAddr("ip", dest)
	if err != nil {
		return 0, fmt.Errorf("resolve %s: %w", dest, err)
	}

	// PMTU currently only supported for IPv4
	if dstAddr.IP.To4() == nil {
		return p.Size, fmt.Errorf("PMTU discovery not supported for IPv6")
	}

	// No need to initialize p.conn here as we use fresh connections for probing in canSendPayload

	low := min
	if low > start {
		low = start
	}
	high := start
	for low < high {
		mid := (low + high + 1) / 2
		ok, err := canSendPayloadFn(p, dstAddr, mid)
		if err != nil {
			return 0, err
		}
		if ok {
			if logf != nil {
				logf(fmt.Sprintf("[PMTU] payload=%d OK", mid))
			}
			low = mid
		} else {
			if logf != nil {
				logf(fmt.Sprintf("[PMTU] payload=%d FAIL", mid))
			}
			high = mid - 1
		}
	}
	return low, nil
}

func (p *Pinger) canSendPayload(dstAddr *net.IPAddr, payloadLen int) (bool, error) {
	if payloadLen < 0 {
		payloadLen = 0
	}

	payload := buildPayload(payloadLen)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   p.baseID & 0xffff,
			Seq:  0,
			Data: payload,
		},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return false, err
	}

	bindAddr := "0.0.0.0"
	if p.Source != "" {
		bindAddr = p.Source
	}

	c, err := p.listenPacket("ip4:icmp", bindAddr)
	if err != nil {
		return false, err
	}
	defer c.Close()

	rc, err := ipv4.NewRawConn(c)
	if err != nil {
		return false, err
	}

	h := &ipv4.Header{
		Version:  4,
		Len:      ipv4.HeaderLen,
		TotalLen: ipv4.HeaderLen + len(b),
		TTL:      64,
		Protocol: 1,
		Dst:      dstAddr.IP,
		Flags:    ipv4.DontFragment,
	}
	// Note: checking p.Source is redundant if we bind, but RawConn might need Src set
	if p.Source != "" {
		h.Src = net.ParseIP(p.Source)
	}

	if err := rc.WriteTo(h, b, nil); err != nil {
		if isMTUTooLarge(err) {
			return false, nil
		}
		return false, err
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	buf := make([]byte, 1500)
	for {
		_ = rc.SetReadDeadline(deadline)
		_, pld, _, err := rc.ReadFrom(buf)
		if err != nil {
			if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
				return false, nil
			}
			continue
		}
		parsed, err := icmp.ParseMessage(1, pld)
		if err != nil {
			continue
		}
		switch parsed.Type {
		case ipv4.ICMPTypeEchoReply:
			if echo, ok := parsed.Body.(*icmp.Echo); ok {
				if echo.ID == (p.baseID&0xffff) && echo.Seq == 0 {
					return true, nil
				}
			}
		case ipv4.ICMPTypeDestinationUnreachable:
			if parsed.Code == 4 {
				return false, nil
			}
		}
	}
}

func (p *Pinger) TraceRoute(dest string, maxHops int, timeout time.Duration) ([]string, error) {
	if dest == "" {
		return nil, fmt.Errorf("destination is empty")
	}
	if maxHops <= 0 {
		return nil, fmt.Errorf("maxHops must be > 0")
	}
	dstAddr, err := p.resolveIPAddr("ip", dest)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", dest, err)
	}

	isV4 := dstAddr.IP.To4() != nil

	// Setup connection
	var connV4 *ipv4.PacketConn
	var connV6 *ipv6.PacketConn
	var packetConn net.PacketConn

	if isV4 {
		bindAddr := "0.0.0.0"
		if p.Source != "" {
			bindAddr = p.Source
		}
		c, err := p.listenPacket("ip4:icmp", bindAddr)
		if err != nil {
			return nil, err
		}
		packetConn = c
		connV4 = ipv4.NewPacketConn(c)
	} else {
		bindAddr := "::"
		if p.Source != "" {
			bindAddr = p.Source
		}
		c, err := p.listenPacket("ip6:ipv6-icmp", bindAddr)
		if err != nil {
			return nil, err
		}
		packetConn = c
		connV6 = ipv6.NewPacketConn(c)
		// Enable HopLimit receiving
		connV6.SetControlMessage(ipv6.FlagHopLimit, true)
	}
	defer packetConn.Close()

	traceID := (p.baseID + 0x1234 + (time.Now().Nanosecond() & 0x3fff)) & 0xffff
	traceTag := fmt.Sprintf("TRC-%04x", traceID)
	payload := []byte(traceTag)
	hops := make([]string, 0, maxHops)
	buf := make([]byte, 1500)

	for ttl := 1; ttl <= maxHops; ttl++ {
		var msg icmp.Message
		if isV4 {
			msg = icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{ID: traceID, Seq: ttl, Data: payload},
			}
		} else {
			msg = icmp.Message{
				Type: ipv6.ICMPTypeEchoRequest,
				Code: 0,
				Body: &icmp.Echo{ID: traceID, Seq: ttl, Data: payload},
			}
		}
		b, err := msg.Marshal(nil)
		if err != nil {
			return hops, err
		}

		// Send
		if isV4 {
			cm := &ipv4.ControlMessage{TTL: ttl}
			if _, err := connV4.WriteTo(b, cm, dstAddr); err != nil {
				hops = append(hops, "*")
				continue
			}
		} else {
			cm := &ipv6.ControlMessage{HopLimit: ttl}
			if _, err := connV6.WriteTo(b, cm, dstAddr); err != nil {
				hops = append(hops, "*")
				continue
			}
		}

		// Read Loop
		deadline := time.Now().Add(timeout)
		if isV4 {
			connV4.SetReadDeadline(deadline)
		} else {
			connV6.SetReadDeadline(deadline)
		}

		for {
			var n int
			var src net.Addr
			var err error

			if isV4 {
				n, _, src, err = connV4.ReadFrom(buf)
			} else {
				n, _, src, err = connV6.ReadFrom(buf)
			}

			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
					hops = append(hops, "*")
					break
				}
				continue // retry reading until timeout
			}

			// Parse
			proto := 1
			if !isV4 {
				proto = 58
			}
			parsed, err := icmp.ParseMessage(proto, buf[:n])
			if err != nil {
				continue
			}

			// Check if relevant
			isReply := false
			isTimeExceeded := false
			isUnreachable := false
			
			switch parsed.Type {
			case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
				if echo, ok := parsed.Body.(*icmp.Echo); ok {
					if echo.ID == traceID && echo.Seq == ttl {
						isReply = true
					}
				}
			case ipv4.ICMPTypeTimeExceeded, ipv6.ICMPTypeTimeExceeded:
				id, seq, ok := extractEchoIDSeq(parsed)
				if ok && id == traceID && seq == ttl {
					isTimeExceeded = true
				}
			case ipv4.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeDestinationUnreachable:
				id, seq, ok := extractEchoIDSeq(parsed)
				if ok && id == traceID && seq == ttl {
					isUnreachable = true
				}
			}

			if isReply || isTimeExceeded || isUnreachable {
				hops = append(hops, src.String())
				if isReply || isUnreachable {
					return hops, nil
				}
				break // Proceed to next TTL
			}
		}
	}
	return hops, nil
}

func isMTUTooLarge(err error) bool {
	if errors.Is(err, syscall.EMSGSIZE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "message too long") || strings.Contains(msg, "EMSGSIZE")
}

func (p *Pinger) Start(privileged bool, interval, timeout time.Duration) error {
	var errV4, errV6 error

	// Initialize IPv4
	if p.Source == "" || isIPv4(p.Source) {
		network := "ip4:icmp"
		if !privileged {
			network = "udp4"
		}
		bindAddr := "0.0.0.0"
		if p.Source != "" {
			bindAddr = p.Source
		}
		
		c, err := p.listenPacket(network, bindAddr)
		if err == nil {
			p.connV4 = ipv4.NewPacketConn(c)
			if err := p.connV4.SetControlMessage(ipv4.FlagTTL, true); err != nil {
				// Non-fatal
			}
		} else {
			errV4 = err
		}
	}

	// Initialize IPv6
	if p.Source == "" || !isIPv4(p.Source) {
		network := "ip6:ipv6-icmp"
		if !privileged {
			network = "udp6"
		}
		bindAddr := "::"
		if p.Source != "" {
			bindAddr = p.Source
		}

		c, err := p.listenPacket(network, bindAddr)
		if err == nil {
			p.connV6 = ipv6.NewPacketConn(c)
			if err := p.connV6.SetControlMessage(ipv6.FlagHopLimit, true); err != nil {
				// Non-fatal
			}
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
		p.targetChans[id] = make(chan Reply, 100)
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
	close(p.done)
	if p.connV4 != nil {
		p.connV4.Close()
	}
	if p.connV6 != nil {
		p.connV6.Close()
	}
}

func (p *Pinger) runReceiverV4() {
	buf := make([]byte, 65535)
	for {
		select {
		case <-p.done:
			return
		default:
			p.connV4.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, cm, _, err := p.connV4.ReadFrom(buf)
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
					continue
				}
				return
			}

			msg, err := icmp.ParseMessage(1, buf[:n]) // Protocol 1 for ICMPv4
			if err != nil {
				// Try parsing as IP packet if needed (omitted for brevity, usually ParseMessage works with NewPacketConn)
				if len(buf[:n]) > 0 && buf[0] == 0x45 {
					ihl := int(buf[0]&0x0f) * 4
					if n > ihl {
						if msg2, err2 := icmp.ParseMessage(1, buf[ihl:n]); err2 == nil {
							msg = msg2
							err = nil
						}
					}
				}
				if err != nil {
					continue
				}
			}

			switch msg.Type {
			case ipv4.ICMPTypeEchoReply:
				echo, ok := msg.Body.(*icmp.Echo)
				if !ok {
					continue
				}
				p.mapMu.RLock()
				ch, exists := p.targetChans[echo.ID]
				p.mapMu.RUnlock()

				if exists {
					ttl := 0
					if cm != nil {
						ttl = cm.TTL
					}
					select {
					case ch <- Reply{TTL: ttl, Seq: echo.Seq}:
					default:
					}
				}
			case ipv4.ICMPTypeDestinationUnreachable, ipv4.ICMPTypeTimeExceeded, ipv4.ICMPTypeParameterProblem:
				id, seq, ok := extractEchoIDSeq(msg)
				if !ok {
					continue
				}
				errMsg := icmpErrorString(msg.Type, msg.Code)
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
		}
	}
}

func (p *Pinger) runReceiverV6() {
	buf := make([]byte, 65535)
	for {
		select {
		case <-p.done:
			return
		default:
			p.connV6.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, cm, _, err := p.connV6.ReadFrom(buf)
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
					continue
				}
				return
			}

			msg, err := icmp.ParseMessage(58, buf[:n]) // Protocol 58 for ICMPv6
			if err != nil {
				continue
			}

			switch msg.Type {
			case ipv6.ICMPTypeEchoReply:
				echo, ok := msg.Body.(*icmp.Echo)
				if !ok {
					continue
				}
				p.mapMu.RLock()
				ch, exists := p.targetChans[echo.ID]
				p.mapMu.RUnlock()

				if exists {
					hopLimit := 0
					if cm != nil {
						hopLimit = cm.HopLimit
					}
					select {
					case ch <- Reply{TTL: hopLimit, Seq: echo.Seq}:
					default:
					}
				}
			case ipv6.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeTimeExceeded, ipv6.ICMPTypeParameterProblem:
				id, seq, ok := extractEchoIDSeq(msg)
				if !ok {
					continue
				}
				errMsg := icmpV6ErrorString(msg.Type, msg.Code)
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
		}
	}
}

func icmpV6ErrorString(typ icmp.Type, code int) string {
	switch typ {
	case ipv6.ICMPTypeDestinationUnreachable:
		return destUnreachV6String(code)
	case ipv6.ICMPTypeTimeExceeded:
		return "Time Exceeded"
	case ipv6.ICMPTypeParameterProblem:
		return "Parameter Problem"
	default:
		return "ICMPv6 Error"
	}
}

func destUnreachV6String(code int) string {
	switch code {
	case 0: return "No Route to Destination"
	case 1: return "Communication with Destination Administratively Prohibited"
	case 3: return "Address Unreachable"
	case 4: return "Port Unreachable"
	default: return "Destination Unreachable"
	}
}


func extractEchoIDSeq(msg *icmp.Message) (int, int, bool) {
	switch body := msg.Body.(type) {
	case *icmp.DstUnreach:
		return parseInnerEchoIDSeq(body.Data)
	case *icmp.TimeExceeded:
		return parseInnerEchoIDSeq(body.Data)
	case *icmp.ParamProb:
		return parseInnerEchoIDSeq(body.Data)
	default:
		return 0, 0, false
	}
}

func parseInnerEchoIDSeq(data []byte) (int, int, bool) {
	if len(data) < 1 {
		return 0, 0, false
	}
	version := data[0] >> 4

	if version == 4 {
		ihl := int(data[0]&0x0f) * 4
		if ihl <= 0 || len(data) < ihl {
			return 0, 0, false
		}
		inner, err := icmp.ParseMessage(1, data[ihl:])
		if err != nil {
			return 0, 0, false
		}
		echo, ok := inner.Body.(*icmp.Echo)
		if !ok {
			return 0, 0, false
		}
		return echo.ID, echo.Seq, true
	} else if version == 6 {
		// IPv6 header is 40 bytes.
		// We assume no extension headers for simplicity in this context.
		// A more robust implementation would parse the Next Header chain.
		const ipv6HeaderLen = 40
		if len(data) < ipv6HeaderLen {
			return 0, 0, false
		}
		// Protocol 58 for ICMPv6
		inner, err := icmp.ParseMessage(58, data[ipv6HeaderLen:])
		if err != nil {
			return 0, 0, false
		}
		echo, ok := inner.Body.(*icmp.Echo)
		if !ok {
			return 0, 0, false
		}
		return echo.ID, echo.Seq, true
	}

	return 0, 0, false
}

func icmpErrorString(typ icmp.Type, code int) string {
	switch typ {
	case ipv4.ICMPTypeDestinationUnreachable:
		return destUnreachString(code)
	case ipv4.ICMPTypeTimeExceeded:
		return timeExceededString(code)
	case ipv4.ICMPTypeParameterProblem:
		return paramProblemString(code)
	default:
		return "ICMP Error"
	}
}

func destUnreachString(code int) string {
	switch code {
	case 0:
		return "Destination Network Unreachable"
	case 1:
		return "Destination Host Unreachable"
	case 2:
		return "Destination Protocol Unreachable"
	case 3:
		return "Destination Port Unreachable"
	case 4:
		return "Fragmentation Needed"
	case 5:
		return "Source Route Failed"
	case 6:
		return "Destination Network Unknown"
	case 7:
		return "Destination Host Unknown"
	case 8:
		return "Source Host Isolated"
	case 9:
		return "Network Administratively Prohibited"
	case 10:
		return "Host Administratively Prohibited"
	case 11:
		return "Network Unreachable for ToS"
	case 12:
		return "Host Unreachable for ToS"
	case 13:
		return "Communication Administratively Prohibited"
	case 14:
		return "Host Precedence Violation"
	case 15:
		return "Precedence Cutoff in Effect"
	default:
		return "Destination Unreachable"
	}
}

func timeExceededString(code int) string {
	switch code {
	case 0:
		return "Time Exceeded"
	case 1:
		return "Fragment Reassembly Time Exceeded"
	default:
		return "Time Exceeded"
	}
}

func paramProblemString(code int) string {
	switch code {
	case 0:
		return "Parameter Problem"
	case 1:
		return "Missing Required Option"
	case 2:
		return "Bad Length"
	default:
		return "Parameter Problem"
	}
}

func (p *Pinger) runWorker(t *stats.TargetStats, id int, interval, timeout time.Duration) {
	// Initial resolution using "ip" to support both V4 and V6
	dstAddr, err := p.resolveIPAddr("ip", t.Host)
	if err != nil {
		t.OnFailure("DNS Error")
	} else {
		t.SetIP(dstAddr.String())
	}

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

	// Prepare payload
	payload := buildPayload(p.Size)

	for {
		// Check count limit
		if p.Count > 0 && seq >= p.Count {
			return
		}

		select {
		case <-dnsTicker.C:
			// Re-resolve DNS
			newAddr, err := p.resolveIPAddr("ip", t.Host)
			if err == nil {
				dstAddr = newAddr
				t.SetIP(dstAddr.String())
			}
		default:
		}

		// Check if we have a valid address to send to
		if dstAddr == nil {
			// Try to resolve again immediately if we have no address
			addr, err := p.resolveIPAddr("ip", t.Host)
			if err != nil {
				t.OnFailure("DNS Error")
				select {
				case <-p.done:
					return
				case <-ticker.C:
					continue
				}
			}
			dstAddr = addr
			t.SetIP(addr.String())
		}

		seq++

		var msgType icmp.Type
		var writeFunc func([]byte, net.Addr) (int, error)
		isV4 := dstAddr.IP.To4() != nil

		if isV4 {
			msgType = ipv4.ICMPTypeEcho
			if p.connV4 != nil {
				writeFunc = func(b []byte, dst net.Addr) (int, error) {
					return p.connV4.WriteTo(b, nil, dst)
				}
			}
		} else {
			msgType = ipv6.ICMPTypeEchoRequest
			if p.connV6 != nil {
				writeFunc = func(b []byte, dst net.Addr) (int, error) {
					return p.connV6.WriteTo(b, nil, dst)
				}
			}
		}

		if writeFunc == nil {
			errStr := "No Conn"
			if isV4 {
				errStr = "No IPv4 Conn"
			} else {
				errStr = "No IPv6 Conn"
			}
			t.OnFailure(errStr)
			select {
			case <-p.done:
				return
			case <-ticker.C:
				continue
			}
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
			continue
		}

		start := time.Now()
		_, err = writeFunc(b, dstAddr)
		if err != nil {
			p.setLastErr(err)
			errMsg := p.applyLastErrSource(err.Error())
			t.OnFailure(errMsg)
			p.log(t, seq, "SendError", 0, 0, err.Error())

			select {
			case <-p.done:
				return
			case <-ticker.C:
				continue
			}
		}

		t.IncSent()
		
		// Wait for reply
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
		found := false
		for !found {
			select {
			case reply := <-ch:
				if reply.Seq == seq {
					if reply.Err != "" {
						t.OnFailure(reply.Err)
						p.log(t, seq, "ICMPError", 0, 0, reply.Err)
					} else {
						rtt := time.Since(start)
						t.OnSuccess(rtt, reply.TTL)
						p.log(t, seq, "OK", rtt, reply.TTL, "")
					}
					found = true
				}
			case <-timeoutTimer.C:
				errMsg := p.applyLastErrSource("Timeout")
				t.OnFailure(errMsg)
				p.log(t, seq, "Timeout", 0, 0, "Request timed out")
				found = true
			case <-p.done:
				if timeoutTimer != nil {
					timeoutTimer.Stop()
				}
				return
			}
		}
		timeoutTimer.Stop()

		if p.Count > 0 && seq >= p.Count {
			return
		}

		select {
		case <-p.done:
			return
		case <-ticker.C:
			// Next loop
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
	// Embed "MPING" signature at the beginning if size permits
	if len(payload) >= 5 {
		copy(payload, "MPING")
	}
	return payload
}
