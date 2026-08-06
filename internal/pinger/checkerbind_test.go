package pinger

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// unassignableSource is a TEST-NET-1 (RFC 5737) address that is guaranteed not
// to be configured on the host running the tests. Binding a socket to it fails
// with EADDRNOTAVAIL, which is what lets the behavioral tests below prove -S is
// actually applied to the dial: a checker that ignores Source would reach the
// loopback listener just fine.
const unassignableSource = "192.0.2.5"

// listenLoopbackTCP starts a TCP listener on 127.0.0.1 that accepts and
// immediately closes connections, and returns its address.
func listenLoopbackTCP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start test listener: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	return ln.Addr().String()
}

// ---- PortChecker binding ----

// TestNewPortChecker_ExposesBindConfig verifies the constructor carries -S/-I
// through to the checker, so callers (and cmd/main's wiring tests) can confirm
// the port checks are configured for the same egress path as ICMP.
func TestNewPortChecker_ExposesBindConfig(t *testing.T) {
	bind := BindConfig{Source: "192.0.2.5", Interface: "eth0"}
	pc := NewPortChecker(nil, nil, time.Second, time.Second, bind)

	if got := pc.BindConfig(); got != bind {
		t.Errorf("BindConfig() = %+v, want %+v", got, bind)
	}
}

// TestNewPortChecker_DialersHonourBindConfig verifies both per-protocol
// dialers are built from the BindConfig: the TCP dialer needs a *net.TCPAddr
// LocalAddr and the UDP dialer a *net.UDPAddr, and both need the interface
// Control hook. Before this wiring existed the checker used a bare
// &net.Dialer{Timeout: timeout} for both, so -S/-I never reached the socket.
func TestNewPortChecker_DialersHonourBindConfig(t *testing.T) {
	bind := BindConfig{Source: "192.0.2.5", Interface: "eth0"}
	pc := NewPortChecker(nil, nil, time.Second, 3*time.Second, bind)

	tcpAddr, ok := pc.tcpDialer.LocalAddr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("tcpDialer.LocalAddr: got %T, want *net.TCPAddr", pc.tcpDialer.LocalAddr)
	}
	if !tcpAddr.IP.Equal(net.ParseIP("192.0.2.5")) {
		t.Errorf("tcpDialer.LocalAddr IP: got %v, want 192.0.2.5", tcpAddr.IP)
	}
	udpAddr, ok := pc.udpDialer.LocalAddr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("udpDialer.LocalAddr: got %T, want *net.UDPAddr", pc.udpDialer.LocalAddr)
	}
	if !udpAddr.IP.Equal(net.ParseIP("192.0.2.5")) {
		t.Errorf("udpDialer.LocalAddr IP: got %v, want 192.0.2.5", udpAddr.IP)
	}
	if pc.tcpDialer.Control == nil || pc.udpDialer.Control == nil {
		t.Error("expected both dialers to carry an interface-binding Control hook")
	}
	if pc.tcpDialer.Timeout != 3*time.Second || pc.udpDialer.Timeout != 3*time.Second {
		t.Errorf("dial timeouts: got tcp=%v udp=%v, want both 3s", pc.tcpDialer.Timeout, pc.udpDialer.Timeout)
	}
}

// TestPortChecker_TCPCheckBindsSourceAddress is the behavioral proof for -S on
// TCP: the very same loopback listener reports Open with no binding requested
// and stops being reachable once the checker is told to source from an address
// the host does not own.
func TestPortChecker_TCPCheckBindsSourceAddress(t *testing.T) {
	addr := listenLoopbackTCP(t)

	unbound := NewPortChecker(nil, nil, time.Second, time.Second, BindConfig{})
	if status, _ := checkTCP(context.Background(), unbound.tcpDialer, addr); status != "Open" {
		t.Fatalf("unbound checkTCP: got %q, want Open (test listener unreachable?)", status)
	}

	bound := NewPortChecker(nil, nil, time.Second, time.Second, BindConfig{Source: unassignableSource})
	if status, _ := checkTCP(context.Background(), bound.tcpDialer, addr); status == "Open" {
		t.Errorf("checkTCP with Source=%s: got Open, want a failure — the source address was ignored", unassignableSource)
	}
}

// TestPortChecker_UDPCheckBindsSourceAddress is the UDP counterpart: binding to
// an address the host does not own must fail the dial, which checkUDP reports
// as "Error".
func TestPortChecker_UDPCheckBindsSourceAddress(t *testing.T) {
	bound := NewPortChecker(nil, nil, time.Second, 200*time.Millisecond, BindConfig{Source: unassignableSource})

	status, _ := checkUDP(context.Background(), bound.udpDialer, "127.0.0.1:54321", 200*time.Millisecond)
	if status != "Error" {
		t.Errorf("checkUDP with Source=%s: got %q, want Error — the source address was ignored", unassignableSource, status)
	}
}

// ---- HTTPChecker binding ----

// TestNewHTTPChecker_ExposesBindConfig mirrors the PortChecker accessor test.
func TestNewHTTPChecker_ExposesBindConfig(t *testing.T) {
	bind := BindConfig{Source: "192.0.2.5", Interface: "eth0"}
	hc := NewHTTPChecker(nil, time.Second, time.Second, bind)

	if got := hc.BindConfig(); got != bind {
		t.Errorf("BindConfig() = %+v, want %+v", got, bind)
	}
}

// TestNewHTTPChecker_ZeroBindConfigKeepsDefaultTransport pins the fallback:
// with neither -S nor -I the client must keep using http.DefaultTransport,
// leaving HTTP checks exactly as they behaved before this feature.
func TestNewHTTPChecker_ZeroBindConfigKeepsDefaultTransport(t *testing.T) {
	hc := NewHTTPChecker(nil, time.Second, time.Second, BindConfig{})
	if hc.client.Transport != nil {
		t.Errorf("client.Transport: got %v, want nil (http.DefaultTransport)", hc.client.Transport)
	}
}

// TestNewHTTPChecker_BindConfigInstallsBoundTransport verifies -S/-I cause a
// custom transport to be installed rather than being silently dropped.
func TestNewHTTPChecker_BindConfigInstallsBoundTransport(t *testing.T) {
	hc := NewHTTPChecker(nil, time.Second, time.Second, BindConfig{Interface: "eth0"})
	if hc.client.Transport == nil {
		t.Error("client.Transport: got nil, want a transport whose dials honour the BindConfig")
	}
}

// TestHTTPChecker_SourceAddressBindsRequest is the behavioral proof for -S on
// HTTP: the same httptest server answers normally with no binding requested
// and becomes unreachable once the checker sources from an address the host
// does not own.
func TestHTTPChecker_SourceAddressBindsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	unbound := NewHTTPChecker([]string{srv.URL}, time.Hour, 2*time.Second, BindConfig{})
	unbound.check(unbound.Results()[0])
	if got := unbound.Results()[0].GetView().Status; got != "Up" {
		t.Fatalf("unbound HTTP check: got %q, want Up (test server unreachable?)", got)
	}

	bound := NewHTTPChecker([]string{srv.URL}, time.Hour, 2*time.Second, BindConfig{Source: unassignableSource})
	bound.check(bound.Results()[0])
	if got := bound.Results()[0].GetView().Status; got != "Error" {
		t.Errorf("HTTP check with Source=%s: got %q, want Error — the source address was ignored", unassignableSource, got)
	}
}
