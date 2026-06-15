package pinger

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// parisSeq is the fixed ICMP sequence number used for all Paris Traceroute
// probes. Keeping Seq (and ID) constant across all TTL hops ensures that the
// ICMP checksum — which routers use as part of the ECMP flow hash — stays the
// same for every probe, so all hops are measured along the same network path.
const parisSeq = 0

// acceptParisPacket checks whether a received ICMP message is a valid reply to
// a Paris Traceroute probe identified by traceID.  It returns
// (srcIP, reachedDest, accepted).
//
// Unlike standard traceroute, the Seq field is fixed to parisSeq for all
// probes, so only the ID is used for matching.
func acceptParisPacket(parsed *icmp.Message, src net.Addr, traceID int) (string, bool, bool) {
	srcIP := ""
	switch a := src.(type) {
	case *net.IPAddr:
		srcIP = a.IP.String()
	case *net.UDPAddr:
		srcIP = a.IP.String()
	default:
		if src != nil {
			srcIP = src.String()
		}
	}

	switch parsed.Type {
	case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
		if echo, ok := parsed.Body.(*icmp.Echo); ok {
			if echo.ID == traceID && echo.Seq == parisSeq {
				return srcIP, true, true
			}
		}
	case ipv4.ICMPTypeTimeExceeded, ipv6.ICMPTypeTimeExceeded:
		id, seq, ok := extractEchoIDSeq(parsed)
		if ok && id == traceID && seq == parisSeq {
			return srcIP, false, true
		}
	case ipv4.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeDestinationUnreachable:
		id, seq, ok := extractEchoIDSeq(parsed)
		if ok && id == traceID && seq == parisSeq {
			return srcIP, true, true
		}
	}
	return "", false, false
}

// ParisTraceRoute sends TTL-limited ICMP probes to dest using the Paris
// Traceroute algorithm and returns the IP address of each hop up to maxHops.
// Unreachable hops are represented as "*".
//
// Unlike standard TraceRoute, every probe uses a fixed ICMP Sequence (parisSeq)
// and a fixed payload so that the ICMP checksum is constant across hops. This
// keeps all probes on the same ECMP path, producing a consistent route view.
func (p *Pinger) ParisTraceRoute(dest string, maxHops int, timeout time.Duration) ([]string, error) {
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

	var traceCh chan traceMsg
	if (isV4 && p.connV4 != nil) || (!isV4 && p.connV6 != nil) {
		traceCh = p.RegisterTraceChan()
		defer p.UnregisterTraceChan(traceCh)
	}

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
			return nil, fmt.Errorf("open paris traceroute send socket (v4): %w", err)
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
			return nil, fmt.Errorf("open paris traceroute send socket (v6): %w", err)
		}
		sendConn = c
		sendV6 = ipv6.NewPacketConn(c)
		if err := sendV6.SetControlMessage(ipv6.FlagHopLimit, true); err != nil {
			_ = err
		}
	}
	defer sendConn.Close()

	traceID := (p.baseID + 0x1234 + int(p.traceCounter.Add(1))) & 0xffff

	// Fixed payload: traceSignature + ID (2B) + 0x0000 (Seq fixed, no TTL).
	// Keeping the payload constant ensures the ICMP checksum is constant.
	payload := make([]byte, 8)
	copy(payload[0:4], traceSignature)
	payload[4] = byte(traceID >> 8)
	payload[5] = byte(traceID & 0xff)
	// payload[6:8] = 0x0000 (zero, matches parisSeq)

	hops := make([]string, 0, maxHops)
	buf := make([]byte, probeBufferSize)

	for ttl := 1; ttl <= maxHops; ttl++ {
		var probeMsg icmp.Message
		if isV4 {
			probeMsg = icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{ID: traceID, Seq: parisSeq, Data: payload},
			}
		} else {
			probeMsg = icmp.Message{
				Type: ipv6.ICMPTypeEchoRequest,
				Code: 0,
				Body: &icmp.Echo{ID: traceID, Seq: parisSeq, Data: payload},
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
			timer := time.NewTimer(timeout)
		recvLoop:
			for {
				select {
				case tm := <-traceCh:
					srcIP, reached, accepted := acceptParisPacket(tm.parsed, tm.src, traceID)
					if accepted {
						if srcIP == "" {
							srcIP = "*"
						} else if srcIP != "*" && p.AsnEnabled {
							if asn := p.getASN(srcIP); asn != "" {
								srcIP = fmt.Sprintf("%s(%s)", srcIP, asn)
							}
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
				srcIP, reached, accepted := acceptParisPacket(parsed, src, traceID)
				if accepted {
					if srcIP == "" {
						srcIP = "*"
					} else if srcIP != "*" && p.AsnEnabled {
						if asn := p.getASN(srcIP); asn != "" {
							srcIP = fmt.Sprintf("%s(%s)", srcIP, asn)
						}
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
