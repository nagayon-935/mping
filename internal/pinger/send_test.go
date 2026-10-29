package pinger

import (
	"errors"
	"net"
	"syscall"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/nagayon-935/mping/internal/stats"
)

func TestBuildPayload(t *testing.T) {
	t.Run("carries the signature then the fill pattern", func(t *testing.T) {
		got := buildPayload(8)
		if len(got) != 8 {
			t.Fatalf("payload len: got %d", len(got))
		}
		if string(got[:5]) != "MPING" {
			t.Fatalf("signature: got %q", string(got[:5]))
		}
		if got[5] != 'A' {
			t.Fatalf("payload fill: got %q", got[5])
		}
	})

	t.Run("negative size yields an empty payload", func(t *testing.T) {
		if got := buildPayload(-1); len(got) != 0 {
			t.Fatalf("expected empty payload for negative size, got len=%d", len(got))
		}
	})
}

func TestApplyLastErrSource(t *testing.T) {
	const raw = "write ip 0.0.0.0->8.8.8.8: sendmsg: no route to host"
	tests := []struct {
		name   string
		source string
		in     string
		want   string
	}{
		{"substitutes the bound source for 0.0.0.0", "10.0.0.2", raw,
			"write ip 10.0.0.2->8.8.8.8: sendmsg: no route to host"},
		{"no source configured leaves the error alone", "", raw, raw},
		{"unrelated error is untouched", "10.0.0.2", "connection refused", "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{})
			p.Source = tt.source
			if got := p.applyLastErrSource(tt.in); got != tt.want {
				t.Fatalf("applyLastErrSource = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetWriteFunc(t *testing.T) {
	v4Dst := &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}
	v6Dst := &net.IPAddr{IP: net.ParseIP("2001:db8::1")}

	t.Run("IPv4 with conn", func(t *testing.T) {
		p := NewPinger(nil)
		conn := &fakePacketConn{}
		p.connV4 = conn
		msgType, writeFunc, errStr := p.getWriteFunc(v4Dst, dscpUnset, false)
		if writeFunc == nil || errStr != "" {
			t.Fatalf("writeFunc nil=%v errStr=%q, want a usable writer", writeFunc == nil, errStr)
		}
		if msgType != ipv4.ICMPTypeEcho {
			t.Fatalf("msgType = %v, want ICMPTypeEcho", msgType)
		}
		if n, err := writeFunc([]byte("test"), v4Dst); err != nil || n != 4 {
			t.Fatalf("writeFunc = (%d, %v), want (4, nil)", n, err)
		}
		if got := conn.writes(); len(got) != 1 || string(got[0]) != "test" {
			t.Fatalf("conn saw writes %q, want [test]", got)
		}
	})

	t.Run("IPv4 without conn", func(t *testing.T) {
		_, writeFunc, errStr := NewPinger(nil).getWriteFunc(v4Dst, dscpUnset, false)
		if writeFunc != nil {
			t.Fatal("expected nil writeFunc")
		}
		if errStr != "No IPv4 Conn" {
			t.Fatalf("errStr = %q, want %q", errStr, "No IPv4 Conn")
		}
	})

	t.Run("IPv6 with conn", func(t *testing.T) {
		p := NewPinger(nil)
		conn := &fakePacketConnV6{}
		p.connV6 = conn
		msgType, writeFunc, errStr := p.getWriteFunc(v6Dst, dscpUnset, false)
		if writeFunc == nil || errStr != "" {
			t.Fatalf("writeFunc nil=%v errStr=%q, want a usable writer", writeFunc == nil, errStr)
		}
		if msgType != ipv6.ICMPTypeEchoRequest {
			t.Fatalf("msgType = %v, want ICMPTypeEchoRequest", msgType)
		}
		if n, err := writeFunc([]byte("test"), v6Dst); err != nil || n != 4 {
			t.Fatalf("writeFunc = (%d, %v), want (4, nil)", n, err)
		}
		if len(conn.writeCMs) != 1 {
			t.Fatalf("conn saw %d writes, want 1", len(conn.writeCMs))
		}
	})

	t.Run("IPv6 without conn", func(t *testing.T) {
		_, writeFunc, errStr := NewPinger(nil).getWriteFunc(v6Dst, dscpUnset, false)
		if writeFunc != nil {
			t.Fatal("expected nil writeFunc")
		}
		if errStr != "No IPv6 Conn" {
			t.Fatalf("errStr = %q, want %q", errStr, "No IPv6 Conn")
		}
	})
}

func TestSendProbe(t *testing.T) {
	dst := &net.IPAddr{IP: net.IPv4(1, 1, 1, 1)}

	t.Run("success counts the probe as sent", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
		p.connV4 = &fakePacketConn{}

		start, ok := p.sendProbe(target, 1, 1, []byte("test"), dst)
		if !ok {
			t.Fatal("expected success")
		}
		if start.IsZero() {
			t.Fatal("expected non-zero start time")
		}
		if view := target.GetView(); view.Sent != 1 {
			t.Fatalf("Sent = %d, want 1", view.Sent)
		}
	})

	t.Run("no conn records a failure", func(t *testing.T) {
		target := stats.NewTargetStats("example.com")
		p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})

		if _, ok := p.sendProbe(target, 1, 1, []byte("test"), dst); ok {
			t.Fatal("expected failure")
		}
		if view := target.GetView(); view.LastError != "No IPv4 Conn" {
			t.Fatalf("LastError = %q, want %q", view.LastError, "No IPv4 Conn")
		}
	})

	t.Run("send errors record a failure and are not counted as sent", func(t *testing.T) {
		for _, sendErr := range []error{errors.New("send failed"), syscall.EPERM} {
			target := stats.NewTargetStats("example.com")
			p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{})
			p.connV4 = &fakePacketConn{writeErr: sendErr}

			if _, ok := p.sendProbe(target, 1, 1, []byte("test"), dst); ok {
				t.Fatalf("%v: expected failure on send error", sendErr)
			}
			view := target.GetView()
			if view.LastError != sendErr.Error() || view.Sent != 0 {
				t.Fatalf("%v: LastError=%q Sent=%d, want the send error and Sent=0", sendErr, view.LastError, view.Sent)
			}
		}
	})
}

// icmpEchoOf parses a captured WriteTo payload back into its echo body.
func icmpEchoOf(t *testing.T, raw []byte) *icmp.Echo {
	t.Helper()
	msg, err := icmp.ParseMessage(1, raw) // protocol 1 = ICMPv4
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	echo, ok := msg.Body.(*icmp.Echo)
	if !ok {
		t.Fatalf("body type = %T, want *icmp.Echo", msg.Body)
	}
	return echo
}
