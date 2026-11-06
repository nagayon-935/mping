package main

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestInterfaceSources_ReachPingerCheckersAndStartup(t *testing.T) {
	for _, tt := range []struct{ name, v4, v6 string }{
		{"dual stack", "192.0.2.10", "fe80::1%test0"},
		{"IPv4 only", "192.0.2.10", ""},
		{"IPv6 only", "", "2001:db8::10"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			orig := getInterfaceIPFn
			getInterfaceIPFn = func(_ string, v6 bool) (string, error) {
				if v6 {
					return tt.v6, nil
				}
				return tt.v4, nil
			}
			t.Cleanup(func() { getInterfaceIPFn = orig })
			cfg := config{ifaceName: "test0"}
			var out, errOut bytes.Buffer

			env, code, ok := prepareRunEnv(cfg, nil, &out, &errOut)
			bind := checkerBindConfig(cfg, env.bindIP)
			var created *pinger.Pinger
			origNew := newPinger
			newPinger = func([]*stats.TargetStats, pinger.Options) pingerController {
				created = pinger.NewPingerWithOptions(nil, pinger.Options{})
				return &pingerAdapter{Pinger: created}
			}
			t.Cleanup(func() { newPinger = origNew })
			makePingerFactory(nil, pinger.Options{}, cfg, bind)(56)

			if !ok || code != 0 || env.bindIP != "" || bind.SourceV4 != tt.v4 || bind.SourceV6 != tt.v6 {
				t.Fatalf("env=%+v bind=%+v code=%d ok=%v stderr=%s", env, bind, code, ok, errOut.String())
			}
			if created.Source != "" || created.Interface != "test0" || created.SourceV4 != tt.v4 || created.SourceV6 != tt.v6 {
				t.Fatalf("pinger binding=%s/%s/%s/%s", created.Source, created.Interface, created.SourceV4, created.SourceV6)
			}
			for _, address := range []string{tt.v4, tt.v6} {
				if address != "" && !strings.Contains(out.String(), address) {
					t.Fatalf("startup message omits %s: %s", address, out.String())
				}
			}
			if got := resolverBindConfig(cfg, nil); got != bind {
				t.Fatalf("DNS binding=%+v, want %+v", got, bind)
			}
		})
	}
}

func TestInterfaceSources_ExplicitSourceWinsWithoutInterfaceLookup(t *testing.T) {
	orig := getInterfaceIPFn
	getInterfaceIPFn = func(string, bool) (string, error) {
		t.Error("explicit -S should not require interface address detection")
		return "", nil
	}
	t.Cleanup(func() { getInterfaceIPFn = orig })
	cfg := config{ifaceName: "test0", sourceAddr: "192.0.2.99"}

	bind, v4, v6, err := determineSourceIPs(cfg, nil)
	checker := checkerBindConfig(cfg, bind)

	if err != nil || bind != cfg.sourceAddr || v4 != cfg.sourceAddr || v6 != "" || checker.Source != cfg.sourceAddr {
		t.Fatalf("bind=%s v4=%s v6=%s checker=%+v error=%v", bind, v4, v6, checker, err)
	}
}

func TestDetermineSourceIPs_InterfaceAddressesSurviveUnroutableTargets(t *testing.T) {
	orig := getInterfaceIPFn
	getInterfaceIPFn = func(_ string, v6 bool) (string, error) {
		if v6 {
			return "2001:db8::10", nil
		}
		return "192.0.2.10", nil
	}
	t.Cleanup(func() { getInterfaceIPFn = orig })
	stubInterfaceByName(t, func(string) (*net.Interface, error) { return nil, net.UnknownNetworkError("unroutable") })

	bind, v4, v6, err := determineSourceIPs(config{ifaceName: "test0"}, []targetSpec{{Host: "bad host"}})

	if err != nil || bind != "" || v4 != "192.0.2.10" || v6 != "2001:db8::10" {
		t.Fatalf("bind=%s v4=%s v6=%s error=%v", bind, v4, v6, err)
	}
}
