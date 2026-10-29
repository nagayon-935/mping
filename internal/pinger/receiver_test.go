package pinger

import (
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/nagayon-935/mping/internal/stats"
)

// TestRunReceiver_DispatchesToTargetChannel drives real ICMP messages through
// the unified receiver loop (runReceiverV4/V6 -> handleEchoReply /
// handleICMPError) with a fake socket and checks what reaches the target's
// reply channel.
func TestRunReceiver_DispatchesToTargetChannel(t *testing.T) {
	tests := []struct {
		name     string
		v6       bool
		msg      func(id int) icmp.Message
		wantSeq  int
		wantTTL  int    // echo replies only
		wantDSCP int    // echo replies only
		wantErr  string // empty means an echo reply is expected
	}{
		{
			// IPv4 replies never carry an observed DSCP: x/net's
			// ipv4.ControlMessage has no TOS field, so it is always 0.
			name: "v4 echo reply",
			msg: func(id int) icmp.Message {
				return icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: &icmp.Echo{ID: id, Seq: 7, Data: []byte("x")}}
			},
			wantSeq: 7, wantTTL: 55,
		},
		{
			name: "v4 destination unreachable",
			msg: func(id int) icmp.Message {
				return icmp.Message{
					Type: ipv4.ICMPTypeDestinationUnreachable, Code: 1,
					Body: &icmp.DstUnreach{Data: ipPacketWithEcho(false, id, 9)},
				}
			},
			wantSeq: 9, wantErr: "Destination Host Unreachable",
		},
		{
			// The observed TrafficClass byte must reach the target via Reply.DSCP.
			name: "v6 echo reply",
			v6:   true,
			msg: func(id int) icmp.Message {
				return icmp.Message{Type: ipv6.ICMPTypeEchoReply, Body: &icmp.Echo{ID: id, Seq: 7, Data: []byte("x")}}
			},
			wantSeq: 7, wantTTL: 42, wantDSCP: 46 << 2,
		},
		{
			name: "v6 destination unreachable",
			v6:   true,
			msg: func(id int) icmp.Message {
				return icmp.Message{
					Type: ipv6.ICMPTypeDestinationUnreachable, Code: 1,
					Body: &icmp.DstUnreach{Data: ipPacketWithEcho(true, id, 9)},
				}
			},
			wantSeq: 9, wantErr: "Communication with Destination Administratively Prohibited",
		},
		{
			// Before ipv6.ICMPTypePacketTooBig was in receiverV6Config.errorTypes,
			// the receiver silently dropped it and the target showed "Timeout"
			// instead of the real cause.
			name: "v6 packet too big",
			v6:   true,
			msg: func(id int) icmp.Message {
				return icmp.Message{
					Type: ipv6.ICMPTypePacketTooBig,
					Body: &icmp.PacketTooBig{MTU: 1400, Data: ipPacketWithEcho(true, id, 5)},
				}
			},
			wantSeq: 5, wantErr: "Packet Too Big (MTU=1400)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
			id := p.baseID & 0xffff
			ch := make(chan Reply, 1)
			p.targetChans[id] = ch

			msg := tt.msg(id)
			raw, err := msg.Marshal(nil)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			run := p.runReceiverV4
			if tt.v6 {
				p.connV6 = &fakePacketConnV6{readQueue: []readResultV6{
					{data: raw, cm: &ipv6.ControlMessage{HopLimit: 42, TrafficClass: 46 << 2}},
				}}
				run = p.runReceiverV6
			} else {
				p.connV4 = &fakePacketConn{readQueue: []readResult{
					{data: raw, cm: &ipv4.ControlMessage{TTL: 55}},
				}}
			}
			stop, _ := startReceiver(t, p, run)
			defer stop()

			select {
			case got := <-ch:
				if got.Seq != tt.wantSeq || got.Err != tt.wantErr {
					t.Fatalf("reply = %+v, want Seq=%d Err=%q", got, tt.wantSeq, tt.wantErr)
				}
				if tt.wantErr == "" && (got.TTL != tt.wantTTL || got.DSCP != tt.wantDSCP) {
					t.Fatalf("reply = %+v, want TTL=%d DSCP=%d", got, tt.wantTTL, tt.wantDSCP)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for reply")
			}
		})
	}
}

// TestRunReceiverV4_KeepsRunningAfterUnparseablePacket feeds a 1-byte packet
// that icmp.ParseMessage cannot parse; the receiver must skip it and keep
// polling rather than exit.
func TestRunReceiverV4_KeepsRunningAfterUnparseablePacket(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	conn := &fakePacketConn{
		readQueue: []readResult{{data: []byte{0x45}}},
		readDelay: time.Millisecond,
	}
	p.connV4 = conn
	stop, done := startReceiver(t, p, p.runReceiverV4)

	eventually(t, time.Second, "short packet consumed", func() bool {
		conn.mu.Lock()
		defer conn.mu.Unlock()
		return len(conn.readQueue) == 0
	})
	select {
	case <-done:
		t.Fatal("receiver exited after an unparseable packet")
	case <-time.After(10 * time.Millisecond):
	}
	stop()
}

func TestRunReceiver_ExitsOnFatalSocketError(t *testing.T) {
	tests := []struct {
		name string
		conn *fakePacketConn
	}{
		{"non-timeout read error", &fakePacketConn{readErr: errors.New("connection refused")}},
		{"read deadline cannot be set", &fakePacketConn{deadlineErr: errors.New("socket closed")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{})
			p.connV4 = tt.conn
			_, done := startReceiver(t, p, p.runReceiverV4)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runReceiverV4 did not return")
			}
		})
	}
}

func TestHandleEchoReply(t *testing.T) {
	t.Run("matching ID reaches the target with TTL and observed DSCP", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		p := NewPinger([]*stats.TargetStats{target})
		id := p.baseID & 0xffff
		ch := make(chan Reply, 1)
		p.targetChans[id] = ch

		// 184 (0xB8) is the EF DSCP codepoint's TOS byte, standing in for a
		// value the receiver's ControlMessage carried off the reply's IP
		// header (see runReceiverV6's cm.TrafficClass extraction).
		p.handleEchoReply(&icmp.Message{Body: &icmp.Echo{ID: id, Seq: 5}}, 64, 184)

		select {
		case reply := <-ch:
			if reply.TTL != 64 || reply.Seq != 5 || reply.DSCP != 184 {
				t.Fatalf("reply = %+v, want TTL=64 Seq=5 DSCP=184", reply)
			}
		default:
			t.Fatal("expected reply in channel")
		}
	})

	t.Run("full channel drops the reply without blocking", func(t *testing.T) {
		p := NewPinger(nil)
		id := p.baseID & 0xffff
		ch := make(chan Reply, 1)
		ch <- Reply{Seq: 1}
		p.targetChans[id] = ch

		p.handleEchoReply(&icmp.Message{Body: &icmp.Echo{ID: id, Seq: 2}}, 64, 0)

		if got := <-ch; got.Seq != 1 {
			t.Fatalf("channel head = %+v, want the original queued reply", got)
		}
	})

	ignored := map[string]*icmp.Message{
		"unknown ID":    {Body: &icmp.Echo{ID: 9999, Seq: 1}},
		"non-echo body": {Body: &icmp.DstUnreach{Data: []byte("test")}},
	}
	for name, msg := range ignored {
		t.Run(name+" is ignored", func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			p := NewPinger([]*stats.TargetStats{target})
			ch := make(chan Reply, 1)
			p.targetChans[p.baseID&0xffff] = ch

			p.handleEchoReply(msg, 64, 0)

			if len(ch) != 0 {
				t.Fatalf("unexpected reply delivered: %+v", <-ch)
			}
		})
	}
}

func TestHandleICMPError(t *testing.T) {
	tests := []struct {
		name    string
		msg     func(id int) *icmp.Message
		errorFn func(icmp.Type, int) string
		want    *Reply // nil means nothing may be delivered
	}{
		{
			name: "v4 destination unreachable",
			msg: func(id int) *icmp.Message {
				return &icmp.Message{
					Type: ipv4.ICMPTypeDestinationUnreachable, Code: 1,
					Body: &icmp.DstUnreach{Data: ipPacketWithEcho(false, id, 3)},
				}
			},
			errorFn: icmpErrorString,
			want:    &Reply{Seq: 3, Err: "Destination Host Unreachable"},
		},
		{
			// The MTU lives in the message body, not the type/code pair, so
			// handleICMPError layers it over the generic type-level string.
			name: "v6 packet too big carries the MTU",
			msg: func(id int) *icmp.Message {
				return &icmp.Message{
					Type: ipv6.ICMPTypePacketTooBig,
					Body: &icmp.PacketTooBig{MTU: 1280, Data: ipPacketWithEcho(true, id, 8)},
				}
			},
			errorFn: icmpV6ErrorString,
			want:    &Reply{Seq: 8, Err: "Packet Too Big (MTU=1280)"},
		},
		{
			name: "body without an embedded echo is ignored",
			msg: func(int) *icmp.Message {
				return &icmp.Message{
					Type: ipv4.ICMPTypeDestinationUnreachable, Code: 1,
					Body: &icmp.Echo{Data: []byte("test")},
				}
			},
			errorFn: icmpErrorString,
		},
		{
			name: "unknown target ID is ignored",
			msg: func(id int) *icmp.Message {
				return &icmp.Message{
					Type: ipv4.ICMPTypeDestinationUnreachable, Code: 1,
					Body: &icmp.DstUnreach{Data: ipPacketWithEcho(false, id+1, 3)},
				}
			},
			errorFn: icmpErrorString,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPinger([]*stats.TargetStats{stats.NewTargetStats("example.com")})
			id := p.baseID & 0xffff
			ch := make(chan Reply, 1)
			p.targetChans[id] = ch

			p.handleICMPError(tt.msg(id), tt.errorFn)

			select {
			case got := <-ch:
				if tt.want == nil || got != *tt.want {
					t.Fatalf("reply = %+v, want %+v", got, tt.want)
				}
			default:
				if tt.want != nil {
					t.Fatalf("expected reply %+v, got none", *tt.want)
				}
			}
		})
	}
}

func TestIsErrorType(t *testing.T) {
	errorTypes := []icmp.Type{
		ipv4.ICMPTypeDestinationUnreachable,
		ipv4.ICMPTypeTimeExceeded,
		ipv4.ICMPTypeParameterProblem,
	}
	tests := []struct {
		typ  icmp.Type
		want bool
	}{
		{ipv4.ICMPTypeDestinationUnreachable, true},
		{ipv4.ICMPTypeTimeExceeded, true},
		{ipv4.ICMPTypeEchoReply, false},
	}
	for _, tt := range tests {
		if got := isErrorType(tt.typ, errorTypes); got != tt.want {
			t.Errorf("isErrorType(%v) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

// TestReceiverV6ConfigIncludesPacketTooBig pins receiverV6Config.errorTypes
// to include ipv6.ICMPTypePacketTooBig. IPv6 has no "Fragmentation Needed"
// DstUnreach code like IPv4 does (code 4) - Packet Too Big is its own,
// independent ICMPv6 type (type 2) and must be listed explicitly or
// runReceiver's isErrorType check silently drops it, making MTU problems on
// IPv6 look like plain timeouts.
func TestReceiverV6ConfigIncludesPacketTooBig(t *testing.T) {
	if !isErrorType(ipv6.ICMPTypePacketTooBig, receiverV6Config.errorTypes) {
		t.Fatal("receiverV6Config.errorTypes must include ipv6.ICMPTypePacketTooBig")
	}
}

func TestBroadcastTrace_FullChannelDropsNewMessage(t *testing.T) {
	p := NewPinger(nil)
	ch := make(chan traceMsg, 1) // capacity 1, already full
	queued := traceMsg{parsed: &icmp.Message{}}
	ch <- queued

	p.traceChansMu.Lock()
	p.traceChans[0] = ch
	p.traceChansMu.Unlock()

	// Should not block even though ch is full.
	p.broadcastTrace(&icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Body: &icmp.Echo{ID: 0},
	}, &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)})

	if len(ch) != 1 || <-ch != queued {
		t.Fatal("expected the original message to remain and the new one to be dropped")
	}
}
