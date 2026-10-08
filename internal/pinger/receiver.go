package pinger

import (
	"errors"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

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
