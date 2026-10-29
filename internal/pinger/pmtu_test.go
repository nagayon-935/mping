package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// stubCanSendPayload swaps canSendPayloadFn for the duration of the test.
func stubCanSendPayload(t *testing.T, fn func(payloadLen int) (bool, string, error)) {
	t.Helper()
	orig := canSendPayloadFn
	t.Cleanup(func() { canSendPayloadFn = orig })
	canSendPayloadFn = func(_ context.Context, _ *Pinger, _ *net.IPAddr, payloadLen int) (bool, string, error) {
		return fn(payloadLen)
	}
}

func TestDiscoverMaxPayload_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		resolve func(network, address string) (*net.IPAddr, error)
		dest    string
		start   int
	}{
		{"empty destination", resolveTo("1.1.1.1"), "", 1},
		{"non-positive start", resolveTo("1.1.1.1"), "example.com", 0},
		{"resolve failure", func(string, string) (*net.IPAddr, error) { return nil, errors.New("dns fail") }, "example.com", 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{ResolveIPAddr: tt.resolve})
			if _, _, err := p.DiscoverMaxPayload(context.Background(), tt.dest, tt.start, 0, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDiscoverMaxPayload_IPv6NotSupported(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{ResolveIPAddr: resolveTo("2001:db8::1")})
	size, _, err := p.DiscoverMaxPayload(context.Background(), "example.com", 100, 0, nil)
	if err == nil {
		t.Fatal("expected error for ipv6")
	}
	if size != p.Size {
		t.Fatalf("expected size %d, got %d", p.Size, size)
	}
}

func TestDiscoverMaxPayload_BinarySearch(t *testing.T) {
	tests := []struct {
		name       string
		limit      int // largest payload the stubbed path accepts
		start, min int
		want       int
	}{
		{"limit below start", 100, 200, 0, 100},
		// Start well above a typical Ethernet limit so the search crosses it.
		{"ethernet limit far below start", 1472, 9872, 0, 1472},
		{"everything fits", 1 << 20, 50, 0, 50},
		{"negative min is clamped to zero", 1 << 20, 50, -1, 50},
		// min (200) > start (100): low is clamped to start, so no search runs.
		{"min above start", 1 << 20, 100, 200, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubCanSendPayload(t, func(n int) (bool, string, error) { return n <= tt.limit, "", nil })
			p := NewPingerWithOptions(nil, Options{ResolveIPAddr: resolveTo("1.1.1.1")})

			got, _, err := p.DiscoverMaxPayload(context.Background(), "example.com", tt.start, tt.min, nil)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestDiscoverMaxPayload_ProbeErrorAborts(t *testing.T) {
	stubCanSendPayload(t, func(int) (bool, string, error) { return false, "", errors.New("send failed") })
	p := NewPingerWithOptions(nil, Options{ResolveIPAddr: resolveTo("1.1.1.1")})
	if _, _, err := p.DiscoverMaxPayload(context.Background(), "example.com", 100, 0, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverMaxPayload_ContextCancelledStopsSearch(t *testing.T) {
	stubCanSendPayload(t, func(int) (bool, string, error) {
		t.Error("probe sent after the context was cancelled")
		return true, "", nil
	})
	p := NewPingerWithOptions(nil, Options{ResolveIPAddr: resolveTo("1.1.1.1")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := p.DiscoverMaxPayload(ctx, "example.com", 100, 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestDiscoverMaxPayload_BottleneckAndLogging checks that a bottleneck router
// reported on failure is returned and logged, and that plain failures log
// without a bottleneck.
func TestDiscoverMaxPayload_BottleneckAndLogging(t *testing.T) {
	tests := []struct {
		name           string
		bottleneck     string
		wantLogs       []string // substrings that must each appear in some log line
		wantNoMismatch bool
	}{
		{
			name:       "bottleneck reported",
			bottleneck: "10.0.0.1",
			wantLogs:   []string{"OK", "FAIL (mtu mismatch: 10.0.0.1)", "mtu mismatch at: 10.0.0.1"},
		},
		{
			name:           "no bottleneck reported",
			wantLogs:       []string{"OK", "FAIL"},
			wantNoMismatch: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubCanSendPayload(t, func(n int) (bool, string, error) {
				if n <= 100 {
					return true, "", nil
				}
				return false, tt.bottleneck, nil
			})
			p := NewPingerWithOptions(nil, Options{ResolveIPAddr: resolveTo("1.1.1.1")})
			var logs []string

			got, bottleneck, err := p.DiscoverMaxPayload(context.Background(), "example.com", 200, 0, func(l string) { logs = append(logs, l) })

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != 100 || bottleneck != tt.bottleneck {
				t.Fatalf("got (%d, %q), want (100, %q)", got, bottleneck, tt.bottleneck)
			}
			joined := strings.Join(logs, "\n")
			for _, want := range tt.wantLogs {
				if !strings.Contains(joined, want) {
					t.Errorf("logs missing %q:\n%s", want, joined)
				}
			}
			if tt.wantNoMismatch && strings.Contains(joined, "mismatch") {
				t.Errorf("unexpected mismatch log:\n%s", joined)
			}
		})
	}
}

// TestCanSendPayload_FailsBeforeSending covers the failures that happen
// before any packet is on the wire: the ICMP socket cannot be opened, or the
// opened socket cannot be turned into a raw IPv4 connection (the fake
// PacketConn is not a real syscall.Conn). A real raw send needs privileges
// and is not exercised here.
func TestCanSendPayload_FailsBeforeSending(t *testing.T) {
	tests := []struct {
		name       string
		listen     func(network, address string) (net.PacketConn, error)
		payloadLen int
	}{
		{"listen error", func(string, string) (net.PacketConn, error) { return nil, errors.New("listen failed") }, 0},
		// A negative length is normalized to zero before the socket is opened.
		{"listen error with negative payload", func(string, string) (net.PacketConn, error) { return nil, errors.New("listen failed") }, -1},
		{"socket is not a raw connection", listenWith(&fakeNetPacketConn{}), 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{ListenPacket: tt.listen})

			ok, bottleneck, err := p.canSendPayload(context.Background(), &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}, tt.payloadLen)

			if err == nil {
				t.Fatal("expected error")
			}
			if ok || bottleneck != "" {
				t.Fatalf("got ok=%v bottleneck=%q, want false and empty on error", ok, bottleneck)
			}
		})
	}
}

func TestIsMTUTooLarge(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"EMSGSIZE errno", syscall.EMSGSIZE, true},
		{"wrapped EMSGSIZE", fmt.Errorf("write: %w", syscall.EMSGSIZE), true},
		{"message text", errors.New("sendmsg: message too long"), true},
		{"errno name text", errors.New("write ip: EMSGSIZE"), true},
		{"other errno", syscall.EINVAL, false},
		{"unrelated error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMTUTooLarge(tt.err); got != tt.want {
				t.Fatalf("isMTUTooLarge(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestCanSendPayload_ClassifiesTheProbe drives canSendPayload past the raw
// socket through a fake header-included connection: what it writes, and how
// each kind of answer (or its absence) is classified.
func TestCanSendPayload_ClassifiesTheProbe(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{ListenPacket: listenWith(&fakeNetPacketConn{})})
	p.Source = "192.0.2.10"
	id := p.baseID & 0xffff
	router := &ipv4.Header{Src: net.IPv4(192, 0, 2, 1)}
	reply := func(typ icmp.Type, code int, body icmp.MessageBody) rawRead {
		return rawRead{hdr: router, payload: mustMarshal(t, &icmp.Message{Type: typ, Code: code, Body: body})}
	}
	echoReply := func(id int) rawRead {
		return reply(ipv4.ICMPTypeEchoReply, 0, &icmp.Echo{ID: id})
	}
	fragNeeded := func(code, id int) rawRead {
		return reply(ipv4.ICMPTypeDestinationUnreachable, code, &icmp.DstUnreach{Data: ipPacketWithEcho(false, id, 0)})
	}
	tests := []struct {
		name           string
		conn           *fakeRawConn
		wantOK         bool
		wantBottleneck string
		wantErr        bool
	}{
		{"matching echo reply after skipped packets", &fakeRawConn{reads: []rawRead{
			{payload: []byte{0x01}}, // too short to parse
			echoReply(id + 1),       // another prober's reply
			echoReply(id),
		}}, true, "", false},
		{"fragmentation needed names the bottleneck", &fakeRawConn{reads: []rawRead{fragNeeded(4, id)}}, false, "192.0.2.1", false},
		{"fragmentation needed without a header", &fakeRawConn{reads: []rawRead{{payload: fragNeeded(4, id).payload}}}, false, "", false},
		{"another probe's fragmentation needed is ignored", &fakeRawConn{reads: []rawRead{fragNeeded(4, id+1)}}, false, "", false},
		{"other unreachable codes are ignored", &fakeRawConn{reads: []rawRead{fragNeeded(1, id)}}, false, "", false},
		{"no reply before the deadline", &fakeRawConn{}, false, "", false},
		{"EMSGSIZE on send means too large", &fakeRawConn{writeErr: syscall.EMSGSIZE}, false, "", false},
		{"other send errors are returned", &fakeRawConn{writeErr: errors.New("boom")}, false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := newRawConnFn
			t.Cleanup(func() { newRawConnFn = orig })
			newRawConnFn = func(net.PacketConn) (pmtuRawConn, error) { return tt.conn, nil }
			dst := &net.IPAddr{IP: net.IPv4(198, 51, 100, 7)}

			ok, bottleneck, err := p.canSendPayload(context.Background(), dst, 100)

			if (err != nil) != tt.wantErr || ok != tt.wantOK || bottleneck != tt.wantBottleneck {
				t.Fatalf("canSendPayload = (%v, %q, %v), want (%v, %q, error=%v)", ok, bottleneck, err, tt.wantOK, tt.wantBottleneck, tt.wantErr)
			}
			h := tt.conn.header
			if h == nil || h.Flags != ipv4.DontFragment || !h.Dst.Equal(dst.IP) || !h.Src.Equal(net.ParseIP(p.Source)) ||
				h.Protocol != 1 || h.TotalLen != ipv4.HeaderLen+len(tt.conn.payload) {
				t.Fatalf("written header = %+v (payload %d bytes), want DF set from %s to %s", h, len(tt.conn.payload), p.Source, dst)
			}
			msg, perr := icmp.ParseMessage(1, tt.conn.payload)
			if perr != nil {
				t.Fatalf("written payload is not ICMP: %v", perr)
			}
			if echo, isEcho := msg.Body.(*icmp.Echo); !isEcho || echo.ID != id || len(echo.Data) != 100 {
				t.Fatalf("written probe = %+v, want an echo with ID %d and 100 data bytes", msg.Body, id)
			}
		})
	}
}
