//go:build integration

package pinger

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"testing"
	"time"
)

// This opt-in test exercises source binding and URL escaping with IPv6 zones.
// Use an assigned address that supports local TCP/UDP listeners; for example,
// ::1%lo0 on macOS. Self-directed link-local traffic may be filtered by the OS.
// It needs no raw-socket privileges:
//
//	MPING_TEST_IPV6_SOURCE='::1%lo0' NO_PROXY='*' go test -tags integration ./internal/pinger -run TestIntegration_IPv6ScopedSource -v
func TestIntegration_IPv6ScopedSource(t *testing.T) {
	source := os.Getenv("MPING_TEST_IPV6_SOURCE")
	if source == "" {
		t.Skip("set MPING_TEST_IPV6_SOURCE to an assigned scoped IPv6 address")
	}
	address, err := netip.ParseAddr(source)
	if err != nil || !address.Is6() || address.Zone() == "" {
		t.Fatal("MPING_TEST_IPV6_SOURCE must be a scoped IPv6 literal")
	}
	bind := BindConfig{Source: source}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("TCP-and-HTTP", func(t *testing.T) {
		ln, err := net.Listen("tcp6", net.JoinHostPort(source, "0"))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil || peer != source {
				// Some kernels omit the zone in the accepted peer address.
				ip, parseErr := netip.ParseAddr(peer)
				if parseErr != nil || ip.WithZone("") != address.WithZone("") {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		server.Listener.Close()
		server.Listener = ln
		server.Start()
		defer server.Close()
		u := (&url.URL{Scheme: "http", Host: ln.Addr().String()}).String()
		hc := NewHTTPCheckerWithOptions([]string{u}, time.Hour, time.Second, bind, HTTPOptions{})
		defer hc.Stop()
		defer hc.client.CloseIdleConnections()
		hc.check(hc.Results()[0])
		if view := hc.Results()[0].GetView(); view.StatusCode != http.StatusNoContent {
			t.Fatalf("HTTP over scoped TCP: %+v", view)
		}
	})

	t.Run("UDP", func(t *testing.T) {
		ln, err := net.ListenPacket("udp6", net.JoinHostPort(source, "0"))
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		conn, err := NewBoundDialer("udp", time.Second, bind).DialContext(ctx, "udp6", ln.LocalAddr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		deadline := time.Now().Add(time.Second)
		conn.SetDeadline(deadline)
		ln.SetDeadline(deadline)
		if _, err := conn.Write([]byte("v6")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 2)
		n, peer, err := ln.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		if !peer.(*net.UDPAddr).IP.Equal(net.IP(address.AsSlice())) {
			t.Fatalf("peer = %v, want source %s", peer, source)
		}
		if _, err := ln.WriteTo(buf[:n], peer); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "v6" {
			t.Fatalf("UDP echo = %q, err = %v", buf, err)
		}
	})
}
