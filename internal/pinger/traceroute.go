package pinger

import (
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// TraceRoute sends TTL-limited ICMP probes to dest and returns the IP address
// of each hop up to maxHops. Unreachable hops are represented as "*".
// When the pinger's shared receiver is running, it registers a broadcast
// channel to receive Time Exceeded replies without racing with the main
// receiver goroutine.
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
		traceCh = make(chan traceMsg, traceChanBuffer)
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

	traceID := (p.baseID + 0x1234 + int(atomic.AddUint32(&p.traceCounter, 1))) & 0xffff
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
		case ipv4.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeDestinationUnreachable:
			id, seq, ok := extractEchoIDSeq(parsed)
			if ok && id == traceID && seq == ttl {
				return srcIP, true, true
			}
		}
		return "", false, false
	}

	buf := make([]byte, probeBufferSize)

	for ttl := 1; ttl <= maxHops; ttl++ {
		payload := make([]byte, 8)
		copy(payload[0:4], traceSignature)
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
			timer.Stop()
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
