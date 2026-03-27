package pinger

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	receiverBufferSize  = 65535             // buffer size for IPv4/IPv6 receiver goroutines
	probeBufferSize     = 1500              // buffer size for PMTU probe and TraceRoute responses
	replyChanBuffer     = 100              // buffered channel size for ICMP echo replies per target
	traceChanBuffer     = 200              // buffered channel size for TraceRoute messages
	receiverReadTimeout = 1 * time.Second  // read deadline for receiver goroutines (enables done check)
	pmtuProbeTimeout    = 300 * time.Millisecond // read deadline per PMTU probe attempt
	payloadSignature    = "MPING"          // signature embedded at the start of ping payloads
	traceSignature      = "TRC-"           // 4-byte signature embedded in TraceRoute payloads
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
	traceCounter uint32 // atomic counter for unique traceID per concurrent call

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
	buf := make([]byte, receiverBufferSize)
	for {
		select {
		case <-p.done:
			return
		default:
			p.connV4.SetReadDeadline(time.Now().Add(receiverReadTimeout))
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
	buf := make([]byte, receiverBufferSize)
	for {
		select {
		case <-p.done:
			return
		default:
			p.connV6.SetReadDeadline(time.Now().Add(receiverReadTimeout))
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
	// Embed signature at the beginning if size permits
	if len(payload) >= len(payloadSignature) {
		copy(payload, payloadSignature)
	}
	return payload
}
