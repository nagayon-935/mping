package pinger

import (
	"net"
	"syscall"
	"testing"
	"time"
)

// rawBindCall records a single invocation of bindRawConnToInterfaceFn.
type rawBindCall struct {
	ifaceName string
	isIPv6    bool
}

// spyBindRawConnToInterface installs a bindRawConnToInterfaceFn seam that
// records every call instead of touching a real socket, and restores the
// original on t.Cleanup. This mirrors spyBindToInterface in bindif_test.go so
// the dialer-side dispatch can be verified without privileges, real network
// interfaces, or an actual connection.
func spyBindRawConnToInterface(t *testing.T) *[]rawBindCall {
	t.Helper()
	orig := bindRawConnToInterfaceFn
	calls := &[]rawBindCall{}
	bindRawConnToInterfaceFn = func(_ syscall.RawConn, ifaceName string, isIPv6 bool) {
		*calls = append(*calls, rawBindCall{ifaceName: ifaceName, isIPv6: isIPv6})
	}
	t.Cleanup(func() { bindRawConnToInterfaceFn = orig })
	return calls
}

// TestBindConfig_IsZero pins the "no binding requested" predicate the checkers
// use to decide whether to deviate from their pre--S/-I behavior at all.
func TestBindConfig_IsZero(t *testing.T) {
	tests := []struct {
		name string
		bc   BindConfig
		want bool
	}{
		{"empty", BindConfig{}, true},
		{"source only", BindConfig{Source: "192.0.2.5"}, false},
		{"interface only", BindConfig{Interface: "eth0"}, false},
		{"both", BindConfig{Source: "192.0.2.5", Interface: "eth0"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.bc.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNewBoundDialer_SourceSetsLocalAddrPerNetwork verifies that -S produces a
// net.Dialer.LocalAddr of the concrete type the requested network needs: a
// *net.TCPAddr for tcp dials and a *net.UDPAddr for udp dials. Passing the
// wrong concrete type makes DialContext fail with "mismatched local address
// type", so the type is part of the contract, not an implementation detail.
func TestNewBoundDialer_SourceSetsLocalAddrPerNetwork(t *testing.T) {
	tests := []struct {
		network string
		source  string
		wantIP  string
	}{
		{"tcp", "192.0.2.5", "192.0.2.5"},
		{"udp", "192.0.2.5", "192.0.2.5"},
		{"tcp", "2001:db8::1", "2001:db8::1"},
		{"udp", "2001:db8::1", "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.network+"/"+tt.source, func(t *testing.T) {
			d := newBoundDialer(tt.network, time.Second, BindConfig{Source: tt.source})

			var gotIP net.IP
			switch tt.network {
			case "tcp":
				addr, ok := d.LocalAddr.(*net.TCPAddr)
				if !ok {
					t.Fatalf("LocalAddr: got %T, want *net.TCPAddr", d.LocalAddr)
				}
				gotIP = addr.IP
			case "udp":
				addr, ok := d.LocalAddr.(*net.UDPAddr)
				if !ok {
					t.Fatalf("LocalAddr: got %T, want *net.UDPAddr", d.LocalAddr)
				}
				gotIP = addr.IP
			}
			if !gotIP.Equal(net.ParseIP(tt.wantIP)) {
				t.Errorf("LocalAddr IP: got %v, want %v", gotIP, tt.wantIP)
			}
		})
	}
}

// TestNewBoundDialer_NoUsableSourceLeavesLocalAddrNil documents the fallback
// contract: with no -S, or an -S value that isn't a literal IP, the dialer is
// left exactly as it was before this feature existed so the OS picks the
// source address.
func TestNewBoundDialer_NoUsableSourceLeavesLocalAddrNil(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{"empty source", ""},
		{"not an IP", "eth0"},
		{"hostname", "example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newBoundDialer("tcp", time.Second, BindConfig{Source: tt.source})
			if d.LocalAddr != nil {
				t.Errorf("LocalAddr: got %v (%T), want nil", d.LocalAddr, d.LocalAddr)
			}
		})
	}
}

// TestNewBoundDialer_PreservesTimeout guards against the dial timeout being
// dropped while the binding fields are added.
func TestNewBoundDialer_PreservesTimeout(t *testing.T) {
	d := newBoundDialer("tcp", 1234*time.Millisecond, BindConfig{})
	if d.Timeout != 1234*time.Millisecond {
		t.Errorf("Timeout: got %v, want %v", d.Timeout, 1234*time.Millisecond)
	}
}

// TestNewBoundDialer_NoInterfaceLeavesControlNil verifies that without -I the
// dialer installs no Control hook at all, so the socket setup path is
// byte-for-byte the one that shipped before interface binding existed.
func TestNewBoundDialer_NoInterfaceLeavesControlNil(t *testing.T) {
	d := newBoundDialer("tcp", time.Second, BindConfig{Source: "192.0.2.5"})
	if d.Control != nil {
		t.Error("Control: got non-nil, want nil when Interface is unset")
	}
}

// TestNewBoundDialer_ControlDispatchesToBindSeam verifies that -I installs a
// Control hook which forwards the raw socket to the platform-specific bind
// implementation, deriving the address family from the network the dialer is
// actually connecting over.
func TestNewBoundDialer_ControlDispatchesToBindSeam(t *testing.T) {
	tests := []struct {
		network    string
		wantIsIPv6 bool
	}{
		{"tcp4", false},
		{"tcp6", true},
		{"udp4", false},
		{"udp6", true},
	}
	for _, tt := range tests {
		t.Run(tt.network, func(t *testing.T) {
			calls := spyBindRawConnToInterface(t)

			d := newBoundDialer("tcp", time.Second, BindConfig{Interface: "eth0"})
			if d.Control == nil {
				t.Fatal("Control: got nil, want non-nil when Interface is set")
			}
			if err := d.Control(tt.network, "192.0.2.1:80", nil); err != nil {
				t.Fatalf("Control returned error: %v", err)
			}

			if len(*calls) != 1 {
				t.Fatalf("expected exactly 1 bindRawConnToInterfaceFn call, got %d: %+v", len(*calls), *calls)
			}
			got := (*calls)[0]
			if got.ifaceName != "eth0" {
				t.Errorf("ifaceName: got %q, want %q", got.ifaceName, "eth0")
			}
			if got.isIPv6 != tt.wantIsIPv6 {
				t.Errorf("isIPv6: got %v, want %v", got.isIPv6, tt.wantIsIPv6)
			}
		})
	}
}

// TestNewBoundTransport_ZeroBindConfigReturnsNil pins the fallback: when
// neither -S nor -I is given the HTTP checker must keep using
// http.DefaultTransport rather than a clone, leaving prior behavior untouched.
func TestNewBoundTransport_ZeroBindConfigReturnsNil(t *testing.T) {
	if tr := newBoundTransport(time.Second, BindConfig{}); tr != nil {
		t.Errorf("newBoundTransport: got %v, want nil for zero BindConfig", tr)
	}
}

// TestNewBoundTransport_KeepsDefaultTransportSettings verifies the bound
// transport is derived from http.DefaultTransport, so wiring -S/-I does not
// silently drop proxy support or HTTP/2 negotiation.
func TestNewBoundTransport_KeepsDefaultTransportSettings(t *testing.T) {
	tr := newBoundTransport(time.Second, BindConfig{Source: "192.0.2.5"})
	if tr == nil {
		t.Fatal("newBoundTransport: got nil, want a transport for a non-zero BindConfig")
	}
	if tr.DialContext == nil {
		t.Error("DialContext: got nil, want the bound dialer's DialContext")
	}
	if tr.Proxy == nil {
		t.Error("Proxy: got nil, want http.DefaultTransport's proxy function")
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2: got false, want http.DefaultTransport's true")
	}
}
