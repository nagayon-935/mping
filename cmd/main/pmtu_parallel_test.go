package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestSetupPMTU_FailedIPv4DoesNotCapSuccessfulSize(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("192.0.2.1"), stats.NewTargetStats("192.0.2.2")}
	fake := &fakePinger{discoverPath: func(dest string) (pinger.PathMTUResult, error) {
		if dest == "192.0.2.2" {
			return pinger.PathMTUResult{}, io.ErrUnexpectedEOF
		}
		return pinger.PathMTUResult{IP: dest, MTU: 1500, Payload: 1472, Status: "probed"}, nil
	}}
	var errOut bytes.Buffer

	size, _, _ := setupPMTU(func(int) pingerController { return fake }, config{mtuEnabled: true, packetSize: 56}, 1500, targets, &errOut)

	if size != 1472 || targets[1].GetView().PathMTU != 0 || errOut.Len() == 0 {
		t.Fatalf("size=%d failed target PMTU=%d errors=%s; want size 1472, no failed PMTU, diagnostic", size, targets[1].GetView().PathMTU, errOut.String())
	}
}

func TestSetupPMTU_IPv4DiscoveriesOverlap(t *testing.T) {
	var active atomic.Int32
	var overlapped atomic.Bool
	release := make(chan struct{})
	fake := &fakePinger{discoverPath: func(dest string) (pinger.PathMTUResult, error) {
		if active.Add(1) == 2 {
			overlapped.Store(true)
			close(release)
		}
		defer active.Add(-1)
		select {
		case <-release:
		case <-time.After(150 * time.Millisecond):
		}
		return pinger.PathMTUResult{IP: dest, MTU: 1500, Payload: 1472}, nil
	}}
	targets := []*stats.TargetStats{stats.NewTargetStats("192.0.2.1"), stats.NewTargetStats("192.0.2.2")}

	setupPMTU(func(int) pingerController { return fake }, config{mtuEnabled: true, packetSize: 56}, 1500, targets, io.Discard)

	if !overlapped.Load() {
		t.Fatal("IPv4 discoveries did not run concurrently")
	}
}

func TestSetupPMTU_DefersIPv6AndJoinsCancellation(t *testing.T) {
	for _, cancelProbe := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel=", cancelProbe), func(t *testing.T) {
			var calls atomic.Int32
			var factories atomic.Int32
			target := stats.NewTargetStats("fe80::1%test0")
			probes := make(chan *fakePinger, 2)
			factory := func(int) pingerController {
				factories.Add(1)
				p := &fakePinger{discoverPathContext: func(ctx context.Context, dest string) (pinger.PathMTUResult, error) {
					calls.Add(1)
					if dest != "fe80::1%test0" {
						t.Errorf("destination lost zone: %s", dest)
					}
					if cancelProbe {
						<-ctx.Done()
						return pinger.PathMTUResult{}, ctx.Err()
					}
					return pinger.PathMTUResult{IP: dest, MTU: 1450, Payload: 1402, Source: "os", Status: "confirmed"}, nil
				}}
				probes <- p
				return p
			}

			size, _, deferred := setupPMTU(factory, config{mtuEnabled: true, packetSize: 56}, 1500, []*stats.TargetStats{target}, io.Discard)

			if size != 56 || deferred == nil || calls.Load() != 0 {
				t.Fatalf("size=%d deferred=%v discovery calls=%d", size, deferred != nil, calls.Load())
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); deferred(ctx, func(string) {}) }()
			if !pollUntil(time.Second, func() bool { return calls.Load() > 0 }) {
				t.Fatal("deferred discovery did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("deferred discovery ignored cancellation")
			}
			if factories.Load() != 2 {
				t.Fatalf("factory calls=%d, want independent background probe", factories.Load())
			}
			for range 2 {
				if !(<-probes).isReleased() {
					t.Fatal("probe not stopped and closed")
				}
			}
			if !cancelProbe && target.GetView().PathMTU != 1450 {
				t.Fatal("deferred result missing")
			}
			if cancelProbe && target.GetView().PathMTU != 0 {
				t.Fatal("cancelled discovery recorded a result")
			}
		})
	}
}

func TestSetupPMTU_UnresolvableTargetsDoNotAffectFamilyDecision(t *testing.T) {
	fake := &fakePinger{discoverMTU: 1472, resolveTarget: func(_ context.Context, dest string) (*net.IPAddr, error) {
		if dest == "broken" {
			return nil, io.ErrUnexpectedEOF
		}
		return &net.IPAddr{IP: net.ParseIP("192.0.2.1")}, nil
	}}
	targets := []*stats.TargetStats{stats.NewTargetStats("broken"), stats.NewTargetStats("good")}
	var errOut bytes.Buffer

	size, _, deferred := setupPMTU(func(int) pingerController { return fake }, config{mtuEnabled: true, packetSize: 56}, 1500, targets, &errOut)

	if size != 1472 || deferred != nil || targets[0].GetView().PathMTU != 0 || !strings.Contains(errOut.String(), "broken") {
		t.Fatalf("size=%d deferred=%v errors=%s", size, deferred != nil, errOut.String())
	}
}
