package pinger

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func TestStopCancelsAndJoinsContextDNS(t *testing.T) {
	for _, kind := range []string{"address", "ASN", "PTR"} {
		t.Run(kind, func(t *testing.T) {
			entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			blocked := func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				<-release
				return ctx.Err()
			}
			opts := Options{ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
				return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil
			}}
			switch kind {
			case "address":
				opts.ResolveIPAddrContext = func(ctx context.Context, _, _ string) (*net.IPAddr, error) { return nil, blocked(ctx) }
			case "ASN":
				opts.AsnEnabled = true
				opts.LookupTXTContext = func(ctx context.Context, _ string) ([]string, error) { return nil, blocked(ctx) }
			case "PTR":
				opts.PtrEnabled = true
				opts.LookupAddrContext = func(ctx context.Context, _ string) ([]string, error) { return nil, blocked(ctx) }
			}
			target := stats.NewTargetStats("example.com")
			p := NewPingerWithOptions([]*stats.TargetStats{target}, opts)
			p.asnJitter = func() time.Duration { return 0 }
			p.ptrJitter = func() time.Duration { return 0 }
			p.wg.Add(1) // simulate the owning worker without opening a socket
			go func() { defer p.wg.Done(); p.resolveTarget(target) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("lookup did not start")
			}
			p.Stop()
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("Stop did not cancel DNS")
			}
			joined := make(chan struct{})
			go func() { p.Wait(); close(joined) }()
			select {
			case <-joined:
				t.Error("Wait abandoned its DNS operation")
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			select {
			case <-joined:
			case <-time.After(time.Second):
				t.Fatal("Wait did not join DNS")
			}
			if v := target.GetView(); v.Loss != 0 || v.ASN != "" || v.PTR != "" {
				t.Fatalf("cancelled lookup updated stats: %+v", v)
			}
		})
	}
}

type cancelledTransport func(*http.Request) (*http.Response, error)

func (f cancelledTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPStopPreservesLastHealthResult(t *testing.T) {
	hc := NewHTTPChecker([]string{"https://example.com"}, time.Hour, time.Second, BindConfig{})
	r := hc.Results()[0]
	r.SetResult(http.StatusOK, time.Millisecond, nil)
	before := r.GetView()
	entered := make(chan struct{})
	hc.client.Transport = cancelledTransport(func(req *http.Request) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	hc.Start()
	<-entered
	hc.Stop()
	hc.Wait()
	if got := r.GetView(); !reflect.DeepEqual(got, before) {
		t.Fatalf("shutdown changed last HTTP result: %+v", got)
	}
}

func TestStoppedPortCheckerPreservesLastResult(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	target.SetIP("127.0.0.1")
	spec := PortSpec{Port: 443, Protocol: "tcp"}
	pc := NewPortChecker([]*stats.TargetStats{target}, []PortSpec{spec}, time.Hour, time.Second, BindConfig{})
	r := pc.results[0][0]
	r.SetResult("Open", time.Millisecond)
	before := r.GetView()
	pc.Stop()
	pc.check(target, spec, r)
	if got := r.GetView(); !reflect.DeepEqual(got, before) {
		t.Fatalf("shutdown changed port result: %+v", got)
	}
}

func TestContextDNSAfterStopDoesNotStartLookup(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{ResolveIPAddrContext: func(context.Context, string, string) (*net.IPAddr, error) {
		t.Error("lookup started after Stop")
		return nil, nil
	}})
	p.Stop()
	if _, err := p.resolveIPAddrBounded("ip", "example.com"); !errors.Is(err, errPingerStopped) {
		t.Fatalf("err=%v", err)
	}
}
