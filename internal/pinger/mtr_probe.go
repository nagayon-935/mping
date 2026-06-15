package pinger

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// HopReply is the result of a single TTL-limited probe.
type HopReply struct {
	SrcIP       string
	RTT         time.Duration
	ReachedDest bool // true when the destination itself replied
	Responded   bool // false on timeout
}

// hopSendConnV4 is the subset of *ipv4.PacketConn methods used by ProbeHop.
type hopSendConnV4 interface {
	SetTTL(ttl int) error
	WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error)
	SetReadDeadline(t time.Time) error
	ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error)
	Close() error
}

// hopSendConnV6 is the subset of *ipv6.PacketConn methods used by ProbeHop.
type hopSendConnV6 interface {
	SetHopLimit(ttl int) error
	WriteTo(b []byte, cm *ipv6.ControlMessage, dst net.Addr) (int, error)
	SetReadDeadline(t time.Time) error
	ReadFrom(b []byte) (int, *ipv6.ControlMessage, net.Addr, error)
	Close() error
}

// HopSocket holds the send socket and associated traceChan for MTR probing.
// Call Close when done to release resources and deregister the traceChan.
type HopSocket struct {
	sendV4  hopSendConnV4
	sendV6  hopSendConnV6
	traceCh chan traceMsg
	isV4    bool
	pinger  *Pinger
}

// Close releases the socket and deregisters the traceChan from the pinger.
func (s *HopSocket) Close() {
	if s.isV4 && s.sendV4 != nil {
		s.sendV4.Close()
	} else if !s.isV4 && s.sendV6 != nil {
		s.sendV6.Close()
	}
	if s.traceCh != nil {
		s.pinger.UnregisterTraceChan(s.traceCh)
	}
}

// RegisterTraceChan adds ch to the pinger's broadcast list and returns it.
func (p *Pinger) RegisterTraceChan() chan traceMsg {
	ch := make(chan traceMsg, traceChanBuffer)
	p.traceChansMu.Lock()
	p.traceChans = append(p.traceChans, ch)
	p.traceChansMu.Unlock()
	return ch
}

// UnregisterTraceChan removes ch from the pinger's broadcast list.
func (p *Pinger) UnregisterTraceChan(ch chan traceMsg) {
	p.traceChansMu.Lock()
	for i, c := range p.traceChans {
		if c == ch {
			p.traceChans = append(p.traceChans[:i], p.traceChans[i+1:]...)
			break
		}
	}
	p.traceChansMu.Unlock()
}

// NextTraceID returns a unique trace ID for an MTR probe round.
func (p *Pinger) NextTraceID() int {
	return (p.baseID + 0x1234 + int(p.traceCounter.Add(1))) & 0xffff
}

// GetASNFor returns the ASN number string for ip when ASN lookup is enabled.
func (p *Pinger) GetASNFor(ip string) string {
	if !p.AsnEnabled {
		return ""
	}
	return p.getASN(ip)
}

// GetASNInfoFor returns the full ASNInfo for ip when ASN lookup is enabled.
func (p *Pinger) GetASNInfoFor(ip string) ASNInfo {
	if !p.AsnEnabled {
		return ASNInfo{}
	}
	return p.getASNInfo(ip)
}

// OpenHopSocket opens a send socket for TTL-limited probes to dest and
// registers a traceChan so that replies are received via the pinger's shared
// receiver goroutine (avoiding raw-socket races on macOS).
//
// The caller must call HopSocket.Close() when done.
func (p *Pinger) OpenHopSocket(dest string) (*HopSocket, error) {
	dstAddr, err := p.resolveIPAddr("ip", dest)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", dest, err)
	}
	isV4 := dstAddr.IP.To4() != nil

	sock := &HopSocket{isV4: isV4, pinger: p}

	if isV4 {
		bindAddr := "0.0.0.0"
		if p.Source != "" {
			bindAddr = p.Source
		}
		c, err := p.listenPacket("ip4:icmp", bindAddr)
		if err != nil {
			return nil, fmt.Errorf("open MTR send socket (v4): %w", err)
		}
		sock.sendV4 = ipv4.NewPacketConn(c)
	} else {
		bindAddr := "::"
		if p.Source != "" {
			bindAddr = p.Source
		}
		c, err := p.listenPacket("ip6:ipv6-icmp", bindAddr)
		if err != nil {
			return nil, fmt.Errorf("open MTR send socket (v6): %w", err)
		}
		v6conn := ipv6.NewPacketConn(c)
		// Non-fatal: hop limit control message may not be available on all platforms.
		_ = v6conn.SetControlMessage(ipv6.FlagHopLimit, true)
		sock.sendV6 = v6conn
	}

	// Register a traceChan only when the pinger's receiver is running.
	if (isV4 && p.connV4 != nil) || (!isV4 && p.connV6 != nil) {
		sock.traceCh = p.RegisterTraceChan()
	}

	return sock, nil
}

// ProbeHop sends a single TTL-limited ICMP echo probe to dest at the given ttl
// using a previously opened HopSocket, and waits for a reply.
//
// traceID must be unique per probe round (use p.NextTraceID()).
// Returns HopReply with Responded=false on timeout.
func (p *Pinger) ProbeHop(ctx context.Context, sock *HopSocket, dest string, ttl, traceID int, timeout time.Duration) (HopReply, error) {
	dstAddr, err := p.resolveIPAddr("ip", dest)
	if err != nil {
		return HopReply{}, fmt.Errorf("resolve %s: %w", dest, err)
	}

	payload := make([]byte, 8)
	copy(payload[0:4], traceSignature)
	payload[4] = byte(traceID >> 8)
	payload[5] = byte(traceID & 0xff)
	payload[6] = byte(ttl >> 8)
	payload[7] = byte(ttl & 0xff)

	var probeMsg icmp.Message
	if sock.isV4 {
		probeMsg = icmp.Message{
			Type: ipv4.ICMPTypeEcho, Code: 0,
			Body: &icmp.Echo{ID: traceID, Seq: ttl, Data: payload},
		}
	} else {
		probeMsg = icmp.Message{
			Type: ipv6.ICMPTypeEchoRequest, Code: 0,
			Body: &icmp.Echo{ID: traceID, Seq: ttl, Data: payload},
		}
	}
	b, err := probeMsg.Marshal(nil)
	if err != nil {
		return HopReply{}, fmt.Errorf("marshal probe: %w", err)
	}

	sent := time.Now()
	if sock.isV4 {
		_ = sock.sendV4.SetTTL(ttl)
		if _, err := sock.sendV4.WriteTo(b, nil, dstAddr); err != nil {
			return HopReply{}, fmt.Errorf("send probe ttl=%d: %w", ttl, err)
		}
	} else {
		cm := &ipv6.ControlMessage{HopLimit: ttl}
		if _, err := sock.sendV6.WriteTo(b, cm, dstAddr); err != nil {
			return HopReply{}, fmt.Errorf("send probe ttl=%d: %w", ttl, err)
		}
	}

	accept := func(msg *icmp.Message, src net.Addr) (HopReply, bool) {
		return acceptHopPacket(msg, src, traceID, ttl)
	}

	if sock.traceCh != nil {
		return receiveViaTraceChan(ctx, sock.traceCh, accept, sent, timeout)
	}
	return receiveViaSocket(ctx, sock, accept, sent, timeout)
}

// acceptHopPacket checks whether msg is a valid reply for traceID/ttl.
func acceptHopPacket(msg *icmp.Message, src net.Addr, traceID, ttl int) (HopReply, bool) {
	srcIP := extractSrcIP(src)
	switch msg.Type {
	case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
		if echo, ok := msg.Body.(*icmp.Echo); ok && echo.ID == traceID && echo.Seq == ttl {
			return HopReply{SrcIP: srcIP, Responded: true, ReachedDest: true}, true
		}
	case ipv4.ICMPTypeTimeExceeded, ipv6.ICMPTypeTimeExceeded:
		id, seq, ok := extractEchoIDSeq(msg)
		if ok && id == traceID && seq == ttl {
			return HopReply{SrcIP: srcIP, Responded: true}, true
		}
	case ipv4.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeDestinationUnreachable:
		id, seq, ok := extractEchoIDSeq(msg)
		if ok && id == traceID && seq == ttl {
			return HopReply{SrcIP: srcIP, Responded: true, ReachedDest: true}, true
		}
	}
	return HopReply{}, false
}

func extractSrcIP(src net.Addr) string {
	if src == nil {
		return ""
	}
	switch a := src.(type) {
	case *net.IPAddr:
		return a.IP.String()
	case *net.UDPAddr:
		return a.IP.String()
	default:
		return src.String()
	}
}

func receiveViaTraceChan(ctx context.Context, traceCh chan traceMsg, accept func(*icmp.Message, net.Addr) (HopReply, bool), sent time.Time, timeout time.Duration) (HopReply, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return HopReply{}, ctx.Err()
		case tm, ok := <-traceCh:
			if !ok {
				return HopReply{Responded: false}, nil
			}
			if r, matched := accept(tm.parsed, tm.src); matched {
				r.RTT = time.Since(sent)
				return r, nil
			}
		case <-timer.C:
			return HopReply{Responded: false}, nil
		}
	}
}

func receiveViaSocket(ctx context.Context, sock *HopSocket, accept func(*icmp.Message, net.Addr) (HopReply, bool), sent time.Time, timeout time.Duration) (HopReply, error) {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, probeBufferSize)
	proto := 58
	if sock.isV4 {
		proto = 1
		if err := sock.sendV4.SetReadDeadline(deadline); err != nil {
			return HopReply{}, fmt.Errorf("set read deadline: %w", err)
		}
	} else {
		if err := sock.sendV6.SetReadDeadline(deadline); err != nil {
			return HopReply{}, fmt.Errorf("set read deadline: %w", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return HopReply{}, ctx.Err()
		default:
		}
		var n int
		var src net.Addr
		var err error
		if sock.isV4 {
			n, _, src, err = sock.sendV4.ReadFrom(buf)
		} else {
			n, _, src, err = sock.sendV6.ReadFrom(buf)
		}
		if err != nil {
			return HopReply{Responded: false}, nil
		}
		parsed, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		if r, matched := accept(parsed, src); matched {
			r.RTT = time.Since(sent)
			return r, nil
		}
	}
}
