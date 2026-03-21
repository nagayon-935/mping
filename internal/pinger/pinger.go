package pinger

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"github.com/nagayon-935/mping/internal/stats"

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

	connV4      PacketConnV4
	connV6      PacketConnV6
	targetMap   map[int]*stats.TargetStats
	targetChans map[int]chan Reply
	mapMu       sync.RWMutex
	baseID      int

	traceChans   []chan traceMsg // one per concurrent TraceRoute call
	traceChansMu sync.RWMutex

	LogWriter io.Writer // Optional logger

	done chan struct{} // Signal to close receiver
	wg   sync.WaitGroup

	resolveIPAddr resolveIPAddrFunc
	now           func() time.Time
	listenPacket  listenPacketFunc
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

func (p *Pinger) applyLastErrSource(errMsg string) string {
	if p.Source != "" && strings.Contains(errMsg, "write ip 0.0.0.0->") {
		return strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+p.Source+"->", 1)
	}
	return errMsg
}

func (p *Pinger) DiscoverMaxPayload(dest string, start int, min int, logf func(string)) (int, error) {
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

	// Register a channel in traceChans when the pinger's receiver is running for
	// this address family. This avoids the macOS raw-socket race where the pinger's
	// continuous ReadFrom consumes Time Exceeded replies before our socket can see them.
	var traceCh chan traceMsg
	if (isV4 && p.connV4 != nil) || (!isV4 && p.connV6 != nil) {
		traceCh = make(chan traceMsg, 200)
		p.traceChansMu.Lock()
		p.traceChans = append(p.traceChans, traceCh)
		p.traceChansMu.Unlock()
		defer func() {
			p.traceChansMu.Lock()
			for i, ch := range p.traceChans {
				if ch == traceCh {
					p.traceChans = append(p.traceChans[:i], p.traceChans[i+1:]...)
					break
				}
			}
			p.traceChansMu.Unlock()
		}()
	}

	// Open a socket solely for sending TTL-limited probes.
	var sendV4 *ipv4.PacketConn
	var sendV6 *ipv6.PacketConn
	var sendConn net.PacketConn

	if isV4 {
		bindAddr := "0.0.0.0"
		if p.Source != "" {
			bindAddr = p.Source
		}
		c, err := p.listenPacket("ip4:icmp", bindAddr)
		if err != nil {
			return nil, err
		}
		sendConn = c
		sendV4 = ipv4.NewPacketConn(c)
	} else {
		bindAddr := "::"
		if p.Source != "" {
			bindAddr = p.Source
		}
		c, err := p.listenPacket("ip6:ipv6-icmp", bindAddr)
		if err != nil {
			return nil, err
		}
		sendConn = c
		sendV6 = ipv6.NewPacketConn(c)
		sendV6.SetControlMessage(ipv6.FlagHopLimit, true)
	}
	defer sendConn.Close()

	traceID := (p.baseID + 0x1234 + (time.Now().Nanosecond() & 0x3fff)) & 0xffff
	hops := make([]string, 0, maxHops)

	// acceptPacket checks whether a received message is a valid reply to the
	// probe with the given ttl and returns (srcIP, reachedDest, accepted).
	acceptPacket := func(parsed *icmp.Message, src net.Addr, ttl int) (string, bool, bool) {
		srcIP := ""
		if ipAddr, ok := src.(*net.IPAddr); ok {
			srcIP = ipAddr.IP.String()
		} else if udpAddr, ok := src.(*net.UDPAddr); ok {
			srcIP = udpAddr.IP.String()
		} else if src != nil {
			srcIP = src.String()
		}

		switch parsed.Type {
		case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
			if echo, ok := parsed.Body.(*icmp.Echo); ok {
				if echo.ID == traceID && echo.Seq == ttl {
					return srcIP, true, true
				}
			}
		case ipv4.ICMPTypeTimeExceeded, ipv6.ICMPTypeTimeExceeded:
			id, seq, ok := extractEchoIDSeq(parsed)
			if ok && id == traceID && seq == ttl {
				return srcIP, false, true
			}
			if !ok {
				return srcIP, false, true
			}
		case ipv4.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeDestinationUnreachable:
			id, seq, ok := extractEchoIDSeq(parsed)
			if ok && id == traceID && seq == ttl {
				return srcIP, true, true
			}
		}
		return "", false, false
	}

	buf := make([]byte, 1500)

	for ttl := 1; ttl <= maxHops; ttl++ {
		payload := make([]byte, 8)
		copy(payload[0:4], "TRC-")
		payload[4] = byte(traceID >> 8)
		payload[5] = byte(traceID & 0xff)
		payload[6] = byte(ttl >> 8)
		payload[7] = byte(ttl & 0xff)

		var probeMsg icmp.Message
		if isV4 {
			probeMsg = icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{ID: traceID, Seq: ttl, Data: payload},
			}
		} else {
			probeMsg = icmp.Message{
				Type: ipv6.ICMPTypeEchoRequest,
				Code: 0,
				Body: &icmp.Echo{ID: traceID, Seq: ttl, Data: payload},
			}
		}
		b, err := probeMsg.Marshal(nil)
		if err != nil {
			hops = append(hops, "*")
			continue
		}

		if isV4 {
			_ = sendV4.SetTTL(ttl)
			if _, err := sendV4.WriteTo(b, nil, dstAddr); err != nil {
				hops = append(hops, "*")
				continue
			}
		} else {
			cm := &ipv6.ControlMessage{HopLimit: ttl}
			if _, err := sendV6.WriteTo(b, cm, dstAddr); err != nil {
				hops = append(hops, "*")
				continue
			}
		}

		found := false
		reachedDest := false

		if traceCh != nil {
			// Receive via the pinger's shared receiver to avoid socket competition.
			timer := time.NewTimer(timeout)
		recvLoop:
			for {
				select {
				case tm := <-traceCh:
					srcIP, reached, accepted := acceptPacket(tm.parsed, tm.src, ttl)
					if accepted {
						if srcIP == "" {
							srcIP = "*"
						}
						hops = append(hops, srcIP)
						found = true
						reachedDest = reached
						timer.Stop()
						break recvLoop
					}
				case <-timer.C:
					break recvLoop
				}
			}
		} else {
			// Fallback: read directly from our send socket (pinger not running).
			deadline := time.Now().Add(timeout)
			if isV4 {
				sendV4.SetReadDeadline(deadline)
			} else {
				sendV6.SetReadDeadline(deadline)
			}
			for {
				var n int
				var src net.Addr
				if isV4 {
					n, _, src, err = sendV4.ReadFrom(buf)
				} else {
					n, _, src, err = sendV6.ReadFrom(buf)
				}
				if err != nil {
					break
				}
				proto := 1
				if !isV4 {
					proto = 58
				}
				parsed, err := icmp.ParseMessage(proto, buf[:n])
				if err != nil {
					continue
				}
				srcIP, reached, accepted := acceptPacket(parsed, src, ttl)
				if accepted {
					if srcIP == "" {
						srcIP = "*"
					}
					hops = append(hops, srcIP)
					found = true
					reachedDest = reached
					break
				}
			}
		}

		if !found {
			hops = append(hops, "*")
		}
		if reachedDest {
			break
		}
	}
	return hops, nil
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

func isMTUTooLarge(err error) bool {
	if errors.Is(err, syscall.EMSGSIZE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "message too long") || strings.Contains(msg, "EMSGSIZE")
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

func (p *Pinger) runReceiverV4() {
	buf := make([]byte, 65535)
	for {
		select {
		case <-p.done:
			return
		default:
			p.connV4.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, cm, src, err := p.connV4.ReadFrom(buf)
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

			p.broadcastTrace(msg, src)

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
			n, cm, src, err := p.connV6.ReadFrom(buf)
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
					continue
				}
				return
			}

			msg, err := icmp.ParseMessage(58, buf[:n])
			if err != nil {
				continue
			}

			p.broadcastTrace(msg, src)

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
	var data []byte
	switch body := msg.Body.(type) {
	case *icmp.DstUnreach:
		id, seq, ok := parseInnerEchoIDSeq(body.Data)
		if ok { return id, seq, ok }
		data = body.Data
	case *icmp.TimeExceeded:
		id, seq, ok := parseInnerEchoIDSeq(body.Data)
		if ok { return id, seq, ok }
		data = body.Data
	case *icmp.ParamProb:
		id, seq, ok := parseInnerEchoIDSeq(body.Data)
		if ok { return id, seq, ok }
		data = body.Data
	default:
		return 0, 0, false
	}

	// Fallback: Pattern matching for "TRC-" + ID (2B) + Seq (2B)
	for i := 0; i <= len(data)-8; i++ {
		if data[i] == 'T' && data[i+1] == 'R' && data[i+2] == 'C' && data[i+3] == '-' {
			id := int(data[i+4])<<8 | int(data[i+5])
			seq := int(data[i+6])<<8 | int(data[i+7])
			return id, seq, true
		}
	}
	return 0, 0, false
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

		// The inner packet could be ICMP (Protocol 1) or UDP (Protocol 17)
		// if we sent it via a udp4 socket.
		protocol := int(data[9])
		innerData := data[ihl:]

		if protocol == 1 { // ICMP
			inner, err := icmp.ParseMessage(1, innerData)
			if err == nil {
				if echo, ok := inner.Body.(*icmp.Echo); ok {
					return echo.ID, echo.Seq, true
				}
			}
		} else if protocol == 17 { // UDP
			// If we sent ICMP over a UDP socket (non-privileged), 
			// the original packet will have a UDP header (8 bytes).
			if len(innerData) >= 8 {
				// Skip UDP header and try to parse the payload as ICMP
				inner, err := icmp.ParseMessage(1, innerData[8:])
				if err == nil {
					if echo, ok := inner.Body.(*icmp.Echo); ok {
						return echo.ID, echo.Seq, true
					}
				}
			}
		}
		return 0, 0, false
	} else if version == 6 {
		// IPv6 header is 40 bytes.
		const ipv6HeaderLen = 40
		if len(data) < ipv6HeaderLen {
			return 0, 0, false
		}

		protocol := int(data[6]) // Next Header
		innerData := data[ipv6HeaderLen:]

		if protocol == 58 { // ICMPv6
			inner, err := icmp.ParseMessage(58, innerData)
			if err == nil {
				if echo, ok := inner.Body.(*icmp.Echo); ok {
					return echo.ID, echo.Seq, true
				}
			}
		} else if protocol == 17 { // UDP
			if len(innerData) >= 8 {
				inner, err := icmp.ParseMessage(58, innerData[8:])
				if err == nil {
					if echo, ok := inner.Body.(*icmp.Echo); ok {
						return echo.ID, echo.Seq, true
					}
				}
			}
		}
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
