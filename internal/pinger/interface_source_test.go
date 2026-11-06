package pinger

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
)

func TestCanSendPayload_BindsInterfaceAndSource(t *testing.T) {
	for _, source := range []string{"", "192.0.2.99"} {
		t.Run("explicit="+source, func(t *testing.T) {
			var listened string
			p := NewPingerWithOptions(nil, Options{ListenPacket: func(_, address string) (net.PacketConn, error) {
				listened = address
				return &fakeNetPacketConn{}, nil
			}})
			p.Interface, p.Source, p.SourceV4 = "test0", source, "192.0.2.10"
			calls := spyBindToInterface(t)
			raw := &fakeRawConn{}
			orig := newRawConnFn
			t.Cleanup(func() { newRawConnFn = orig })
			newRawConnFn = func(net.PacketConn) (pmtuRawConn, error) { return raw, nil }
			want := source
			if want == "" {
				want = p.SourceV4
			}

			_, _, err := p.canSendPayload(context.Background(), &net.IPAddr{IP: net.ParseIP("198.51.100.1")}, 56, 1234)

			if err != nil || listened != want || raw.header == nil || !raw.header.Src.Equal(net.ParseIP(want)) {
				t.Fatalf("listen=%s header=%+v err=%v, want source %s", listened, raw.header, err, want)
			}
			if len(*calls) != 1 || (*calls)[0].ifaceName != "test0" || (*calls)[0].isIPv6 {
				t.Fatalf("interface bindings=%+v, want test0 IPv4", *calls)
			}
			if raw.header.Flags != ipv4.DontFragment {
				t.Fatal("DF flag lost")
			}
		})
	}
}

func TestListenICMP_UsesInterfaceFamilyOnlyWithoutNativeBinding(t *testing.T) {
	for _, tt := range []struct {
		name                         string
		supported, v6                bool
		explicit, v4, v6Source, want string
		wantErr                      bool
	}{
		{"native v4 chooses source", true, false, "", "192.0.2.10", "2001:db8::10", "0.0.0.0", false},
		{"native v6 chooses source", true, true, "", "192.0.2.10", "2001:db8::10", "::", false},
		{"fallback v4", false, false, "", "192.0.2.10", "2001:db8::10", "192.0.2.10", false},
		{"fallback scoped v6", false, true, "", "192.0.2.10", "fe80::1%test0", "fe80::1%test0", false},
		{"explicit source wins", false, false, "192.0.2.99", "192.0.2.10", "", "192.0.2.99", false},
		{"missing v4 fails", false, false, "", "", "2001:db8::10", "", true},
		{"missing v6 fails", false, true, "", "192.0.2.10", "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			orig := interfaceBindingSupported
			interfaceBindingSupported = tt.supported
			t.Cleanup(func() { interfaceBindingSupported = orig })
			var listened string
			p := NewPingerWithOptions(nil, Options{ListenPacket: func(_, address string) (net.PacketConn, error) {
				listened = address
				return &fakeNetPacketConn{}, nil
			}})
			p.Interface, p.Source, p.SourceV4, p.SourceV6 = "test0", tt.explicit, tt.v4, tt.v6Source

			c, err := p.listenICMP(tt.v6)
			if c != nil {
				c.Close()
			}

			if (err != nil) != tt.wantErr || listened != tt.want {
				t.Fatalf("listen=%q err=%v; want %q error=%v", listened, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestNewBoundDialer_FallbackBindsMatchingFamily(t *testing.T) {
	for _, tt := range []struct {
		network, explicit, v4, v6, want string
		wantErr                         bool
	}{
		{"tcp4", "", "192.0.2.10", "fe80::1%test0", "192.0.2.10", false},
		{"udp6", "", "192.0.2.10", "fe80::1%test0", "fe80::1%test0", false},
		{"tcp6", "", "192.0.2.10", "", "", true},
		{"udp4", "", "", "2001:db8::10", "", true},
		{"tcp4", "192.0.2.99", "192.0.2.10", "", "", false},
	} {
		t.Run(tt.network+"/"+tt.explicit+"/"+tt.want, func(t *testing.T) {
			orig, origBind := interfaceBindingSupported, bindRawConnToSourceFn
			interfaceBindingSupported = false
			t.Cleanup(func() { interfaceBindingSupported, bindRawConnToSourceFn = orig, origBind })
			var bound string
			bindRawConnToSourceFn = func(_ syscall.RawConn, source string, v6 bool) error {
				bound = source
				if v6 != (tt.network[len(tt.network)-1] == '6') {
					t.Error("wrong address family")
				}
				return nil
			}
			d := NewBoundDialer("tcp", time.Second, BindConfig{Interface: "test0", Source: tt.explicit, SourceV4: tt.v4, SourceV6: tt.v6})

			err := d.Control(tt.network, "", nil)

			if (err != nil) != tt.wantErr || bound != tt.want {
				t.Fatalf("bound=%q err=%v; want %q error=%v", bound, err, tt.want, tt.wantErr)
			}
			if tt.explicit == "" && d.LocalAddr != nil {
				t.Fatal("fallback pinned family before Control")
			}
		})
	}
}

func TestIPv6PMTUDialer_FallbackSourceAndExplicitOverride(t *testing.T) {
	for _, tt := range []struct {
		supported                bool
		explicit, fallback, want string
		wantErr                  bool
	}{
		{true, "", "2001:db8::10", "", false},
		{false, "", "fe80::1%test0", "fe80::1%test0", false},
		{false, "2001:db8::99", "2001:db8::10", "2001:db8::99", false},
		{false, "", "", "", true},
	} {
		t.Run(tt.want+"/"+tt.explicit, func(t *testing.T) {
			orig := interfaceBindingSupported
			interfaceBindingSupported = tt.supported
			t.Cleanup(func() { interfaceBindingSupported = orig })
			p := NewPingerWithOptions(nil, Options{})
			p.Interface, p.Source, p.SourceV6 = "test0", tt.explicit, tt.fallback

			d, err := p.ipv6PMTUDialer()

			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, want error=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			got := ""
			if d.LocalAddr != nil {
				got = d.LocalAddr.String()
			}
			if got != tt.want {
				t.Fatalf("LocalAddr=%s, want %s", got, tt.want)
			}
		})
	}
}

func TestSourceSockaddr_PreservesFamilyAndZone(t *testing.T) {
	for _, tt := range []struct {
		source  string
		v6      bool
		wantErr bool
	}{
		{"192.0.2.10", false, false}, {"fe80::1%12", true, false},
		{"", false, true}, {"192.0.2.10", true, true}, {"2001:db8::10", false, true},
		{"fe80::1%mping-no-such-iface", true, true},
	} {
		t.Run(tt.source, func(t *testing.T) {
			addr, err := sourceSockaddr(tt.source, tt.v6)
			if (err != nil) != tt.wantErr {
				t.Fatalf("addr=%v error=%v", addr, err)
			}
			if err != nil {
				return
			}
			switch a := addr.(type) {
			case *syscall.SockaddrInet4:
				if !net.IP(a.Addr[:]).Equal(net.ParseIP(tt.source)) {
					t.Fatal("wrong IPv4 source")
				}
			case *syscall.SockaddrInet6:
				if a.ZoneId != 12 {
					t.Fatalf("zone=%d", a.ZoneId)
				}
			default:
				t.Fatalf("unexpected sockaddr %T", addr)
			}
		})
	}
}

func TestNewBoundDialer_FallbackPropagatesBindFailure(t *testing.T) {
	orig, origBind := interfaceBindingSupported, bindRawConnToSourceFn
	interfaceBindingSupported = false
	t.Cleanup(func() { interfaceBindingSupported, bindRawConnToSourceFn = orig, origBind })
	bindRawConnToSourceFn = func(syscall.RawConn, string, bool) error { return syscall.EADDRNOTAVAIL }
	d := NewBoundDialer("tcp", time.Second, BindConfig{Interface: "test0", SourceV4: "192.0.2.1"})

	err := d.Control("tcp4", "", nil)

	if !errors.Is(err, syscall.EADDRNOTAVAIL) {
		t.Fatalf("error=%v", err)
	}
}
