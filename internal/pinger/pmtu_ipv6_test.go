package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

type fakeIPv6PMTU struct {
	mtu                   int
	queryErr, deadlineErr error
	readErr               error
	onWrite               func(*fakeIPv6PMTU, []byte) error
	packets               [][]byte
	writes                [][]byte
	closed                bool
}

func (f *fakeIPv6PMTU) PathMTU() (int, error)       { return f.mtu, f.queryErr }
func (f *fakeIPv6PMTU) SetDeadline(time.Time) error { return f.deadlineErr }
func (f *fakeIPv6PMTU) Close() error                { f.closed = true; return nil }
func (f *fakeIPv6PMTU) Read(b []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if len(f.packets) == 0 {
		return 0, timeoutOpError()
	}
	packet := f.packets[0]
	f.packets = f.packets[1:]
	return copy(b, packet), nil
}
func (f *fakeIPv6PMTU) Write(b []byte) (int, error) {
	f.writes = append(f.writes, append([]byte(nil), b...))
	if f.onWrite != nil {
		return len(b), f.onWrite(f, b)
	}
	return len(b), nil
}

func echoIPv6PMTU(f *fakeIPv6PMTU, packet []byte) error {
	msg, err := icmp.ParseMessage(58, packet)
	if err != nil {
		return err
	}
	msg.Type = ipv6.ICMPTypeEchoReply
	reply, err := msg.Marshal(nil)
	f.packets = append(f.packets, reply)
	return err
}

func TestDiscoverPathMTUDispatchesOnResolvedIP(t *testing.T) {
	for _, tt := range []struct {
		name, ip, zone, source string
	}{
		{"IPv4", "192.0.2.1", "", "probe"},
		{"IPv4-mapped IPv6", "::ffff:192.0.2.1", "", "probe"},
		{"IPv6", "2001:db8::1", "", "os"},
		{"scoped IPv6", "fe80::1", "en0", "os"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dst := &net.IPAddr{IP: net.ParseIP(tt.ip), Zone: tt.zone}
			lookups, ipv4Probes, ipv6Sockets := 0, 0, 0
			p := NewPingerWithOptions(nil, Options{ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
				lookups++
				if lookups > 1 {
					return nil, errors.New("destination must not be resolved twice")
				}
				return dst, nil
			}})
			defer p.Stop()
			stubCanSendPayload(t, func(size int) (bool, string, error) {
				ipv4Probes++
				return size <= 1400, "", nil
			})
			original := openIPv6PMTUSocket
			t.Cleanup(func() { openIPv6PMTUSocket = original })
			openIPv6PMTUSocket = func(_ context.Context, _ *Pinger, selected *net.IPAddr) (ipv6PMTUSocket, error) {
				ipv6Sockets++
				if selected.String() != dst.String() {
					t.Fatalf("selected destination changed: %v", selected)
				}
				return &fakeIPv6PMTU{mtu: 1500, onWrite: echoIPv6PMTU}, nil
			}
			result, err := p.DiscoverPathMTU(context.Background(), "dual.example.test", 1472, 56, nil)
			if err != nil || lookups != 1 || result.IP != dst.String() || result.Source != tt.source {
				t.Fatalf("result=%+v lookups=%d err=%v", result, lookups, err)
			}
			if tt.source == "probe" {
				if ipv4Probes == 0 || ipv6Sockets != 0 || result.Payload != 1400 || result.MTU != 1428 {
					t.Fatalf("incorrect IPv4 dispatch: %+v probes=%d v6=%d", result, ipv4Probes, ipv6Sockets)
				}
			} else if ipv4Probes != 0 || ipv6Sockets != 1 || result.MTU != 1500 || result.Status != "confirmed" {
				t.Fatalf("incorrect IPv6 dispatch: %+v probes=%d v6=%d", result, ipv4Probes, ipv6Sockets)
			}
		})
	}
}

func TestIPv6PMTUUsesKernelEstimate(t *testing.T) {
	tests := []struct {
		name       string
		write      func(*fakeIPv6PMTU, []byte) error
		wantMTU    int
		wantStatus string
		wantWrites int
	}{
		{"echo confirms full size", echoIPv6PMTU, 1500, "confirmed", 1},
		{"timeout leaves estimate unchanged", nil, 1500, "estimated", 1},
		{"EMSGSIZE without kernel update is only an estimate", func(*fakeIPv6PMTU, []byte) error { return syscall.EMSGSIZE }, 1500, "estimated", 1},
		{"kernel reduction is retried", func(f *fakeIPv6PMTU, b []byte) error {
			if len(f.writes) == 1 {
				f.mtu = 1450
				return syscall.EMSGSIZE
			}
			return echoIPv6PMTU(f, b)
		}, 1450, "confirmed", 2},
		{"PTB triggers a kernel query, not trust in the advertised number", func(f *fakeIPv6PMTU, b []byte) error {
			if len(f.writes) != 1 {
				return echoIPv6PMTU(f, b)
			}
			quoted := make([]byte, 40)
			quoted[0], quoted[6] = 0x60, 58
			quoted = append(quoted, b...)
			ptb, err := (&icmp.Message{Type: ipv6.ICMPTypePacketTooBig, Body: &icmp.PacketTooBig{MTU: 1400, Data: quoted}}).Marshal(nil)
			f.packets = append(f.packets, ptb)
			f.mtu = 1450
			return err
		}, 1450, "confirmed", 2},
		{"bounded retries preserve the latest estimate", func(f *fakeIPv6PMTU, _ []byte) error { f.mtu--; return syscall.EMSGSIZE }, 1492, "estimated", 8},
		{"truncated reply does not confirm", func(f *fakeIPv6PMTU, b []byte) error {
			if err := echoIPv6PMTU(f, b); err != nil {
				return err
			}
			f.packets[0] = f.packets[0][:20]
			return nil
		}, 1500, "estimated", 1},
		{"wrong reply ignored before correct echo", func(f *fakeIPv6PMTU, b []byte) error {
			msg, _ := icmp.ParseMessage(58, b)
			msg.Type = ipv6.ICMPTypeEchoReply
			msg.Body.(*icmp.Echo).Seq++
			wrong, _ := msg.Marshal(nil)
			f.packets = append(f.packets, wrong)
			return echoIPv6PMTU(f, b)
		}, 1500, "confirmed", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{})
			defer p.Stop()
			f := &fakeIPv6PMTU{mtu: 1500, onWrite: tt.write}
			got, err := p.discoverIPv6PathMTU(context.Background(), f, &net.IPAddr{IP: net.ParseIP("2001:db8::1")}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.MTU != tt.wantMTU || got.Payload != tt.wantMTU-48 || got.Status != tt.wantStatus || got.Source != "os" {
				t.Fatalf("result = %+v", got)
			}
			if len(f.writes) != tt.wantWrites {
				t.Fatalf("writes = %d, want %d", len(f.writes), tt.wantWrites)
			}
		})
	}
}

func TestIPv6PMTUErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		socket *fakeIPv6PMTU
		cancel bool
	}{
		{"query fails", &fakeIPv6PMTU{queryErr: errors.New("query failed")}, false},
		{"invalid MTU", &fakeIPv6PMTU{mtu: 1279}, false},
		{"deadline fails", &fakeIPv6PMTU{mtu: 1500, deadlineErr: errors.New("deadline failed")}, false},
		{"write fails", &fakeIPv6PMTU{mtu: 1500, onWrite: func(*fakeIPv6PMTU, []byte) error { return syscall.ENETUNREACH }}, false},
		{"cancelled", &fakeIPv6PMTU{mtu: 1500}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{})
			defer p.Stop()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			if _, err := p.discoverIPv6PathMTU(ctx, tt.socket, &net.IPAddr{IP: net.ParseIP("::1")}, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestIPv6PMTULargeLoopbackAndJumboEstimate(t *testing.T) {
	for _, mtu := range []int{65536, 90000} {
		p := NewPingerWithOptions(nil, Options{})
		f := &fakeIPv6PMTU{mtu: mtu, onWrite: echoIPv6PMTU}
		result, err := p.discoverIPv6PathMTU(context.Background(), f, &net.IPAddr{IP: net.ParseIP("::1")}, nil)
		p.Stop()
		if err != nil {
			t.Fatal(err)
		}
		want := "confirmed"
		if mtu > receiverBufferSize+40 {
			want = "estimated"
		}
		if result.Status != want || result.MTU != mtu {
			t.Fatalf("MTU=%d result=%+v", mtu, result)
		}
	}
}

func TestDiscoverPathMTUIPv6ClosesSocketAndKeepsZone(t *testing.T) {
	original := openIPv6PMTUSocket
	t.Cleanup(func() { openIPv6PMTUSocket = original })
	f := &fakeIPv6PMTU{mtu: 1500, onWrite: echoIPv6PMTU}
	openIPv6PMTUSocket = func(_ context.Context, p *Pinger, dst *net.IPAddr) (ipv6PMTUSocket, error) {
		if dst.Zone != "en0" || p.Source != "fe80::2%en0" || p.Interface != "en0" {
			t.Fatalf("lost destination/source binding: %v, %s, %s", dst, p.Source, p.Interface)
		}
		return f, nil
	}
	p := NewPingerWithOptions(nil, Options{})
	defer p.Stop()
	p.Source, p.Interface = "fe80::2%en0", "en0"
	result, err := p.DiscoverPathMTU(context.Background(), "fe80::1%en0", 1472, 56, nil)
	if err != nil || !f.closed || result.IP != "fe80::1%en0" || result.Status != "confirmed" {
		t.Fatalf("result=%+v closed=%v err=%v", result, f.closed, err)
	}
}

// Direct and wrapped timeouts leave the OS estimate unchanged; other read
// errors still propagate to the caller.
func TestIPv6PMTUReadErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		name    string
		readErr error
		wantErr bool
	}{
		{"socket timeout leaves estimate", timeoutOpError(), false},
		{"wrapped timeout leaves estimate", fmt.Errorf("read: %w", timeoutOpError()), false},
		{"nested wrapped timeout leaves estimate", fmt.Errorf("probe: %w", fmt.Errorf("read: %w", timeoutOpError())), false},
		{"EMSGSIZE leaves estimate", syscall.EMSGSIZE, false},
		{"other read errors are returned", syscall.ENETUNREACH, true},
		{"wrapped non-timeout read errors are returned", fmt.Errorf("read: %w", syscall.ENETUNREACH), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPingerWithOptions(nil, Options{IDs: &IDAllocator{}})
			defer p.Stop()
			f := &fakeIPv6PMTU{mtu: 1500, readErr: tt.readErr}
			result, err := p.discoverIPv6PathMTU(context.Background(), f, &net.IPAddr{IP: net.ParseIP("2001:db8::1")}, nil)
			if tt.wantErr {
				if err != tt.readErr {
					t.Fatalf("error = %v, want original %v", err, tt.readErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.MTU != 1500 || result.Payload != 1452 || result.Status != "estimated" || len(f.writes) != 1 {
				t.Fatalf("result=%+v writes=%d, want unchanged estimate and one probe", result, len(f.writes))
			}
			if len(p.ids.traces) != 0 {
				t.Fatalf("probe ID reservation leaked: %v", p.ids.traces)
			}
		})
	}
}
