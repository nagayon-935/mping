package main

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// TestMakePingerFactory_SetsInterface verifies that the -I interface name
// carried on cfg.ifaceName reaches the pingerController via SetInterface, the
// same way bindIP already reaches it via SetSource — so true interface binding
// (see internal/pinger/bindif_*.go) actually gets armed for CLI-driven runs,
// not just when a caller constructs *pinger.Pinger by hand. When -I was never
// passed the factory still calls SetInterface with an empty string rather than
// skipping the call, matching Pinger's own zero-value semantics (an empty
// Interface is a documented no-op in bindToInterface).
func TestMakePingerFactory_SetsInterface(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config
		bindIP string
		want   string
	}{
		{"-I reaches the pinger", config{ifaceName: "eth0"}, "192.168.1.10", "eth0"},
		{"empty when -I was not given", config{sourceAddr: "10.0.0.5"}, "10.0.0.5", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origNewPinger := newPinger
			t.Cleanup(func() { newPinger = origNewPinger })
			var created *fakePinger
			newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
				created = &fakePinger{}
				return created
			}

			factory := makePingerFactory(nil, pinger.Options{}, tt.cfg, tt.bindIP)
			factory(56)

			if created == nil {
				t.Fatal("expected newPinger to be called")
			}
			if created.interfaceSet != tt.want {
				t.Errorf("SetInterface: got %q, want %q", created.interfaceSet, tt.want)
			}
		})
	}
}

func TestSetupPMTU(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config
		ifaceMTU int
		fake     *fakePinger

		wantFactoryCalled bool
		wantPacketSize    int
		wantErrOut        string // substring; "" means nothing is required
		wantPMTU          int    // PMTU recorded on the target
		wantBottleneck    string // bottleneck IP recorded on the target
		wantNoPreLogs     bool
	}{
		{
			name: "disabled leaves the packet size at its default",
			cfg:  config{mtuEnabled: false}, ifaceMTU: 1500,
			wantPacketSize: 0, wantNoPreLogs: true,
		},
		{
			name: "-m alone records the IPv6 OS estimate and keeps the ping payload",
			cfg:  config{mtuEnabled: true, packetSize: 56}, ifaceMTU: 1500,
			fake: &fakePinger{discoverPath: func(string) (pinger.PathMTUResult, error) {
				return pinger.PathMTUResult{IP: "2001:db8::1", MTU: 1450, Payload: 1402, Source: "os", Status: "estimated"}, nil
			}}, wantFactoryCalled: true,
			wantPacketSize: 56, wantPMTU: 1402, wantNoPreLogs: true,
		},
		{
			name: "discovery failure keeps the configured size",
			cfg:  config{mtuEnabled: true, packetSize: 56}, ifaceMTU: 1500,
			fake: &fakePinger{discoverErr: io.ErrUnexpectedEOF}, wantFactoryCalled: true,
			wantPacketSize: 56, wantErrOut: "PMTU discovery failed", wantNoPreLogs: true,
		},
		{
			name: "success records the PMTU and bottleneck",
			cfg:  config{mtuEnabled: true, packetSize: 56}, ifaceMTU: 1500,
			fake: &fakePinger{discoverMTU: 1400, discoverBottleneckIP: "10.0.0.1"}, wantFactoryCalled: true,
			wantPacketSize: 1400, wantPMTU: 1400, wantBottleneck: "10.0.0.1",
		},
		{
			name: "success without a bottleneck leaves the bottleneck empty",
			cfg:  config{mtuEnabled: true, packetSize: 56}, ifaceMTU: 0,
			fake: &fakePinger{discoverMTU: 1200}, wantFactoryCalled: true,
			wantPacketSize: 1200, wantPMTU: 1200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			makeFn := func(size int) pingerController {
				called = true
				return tt.fake
			}
			targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
			var errOut bytes.Buffer

			packetSize, preLogs := setupPMTU(makeFn, tt.cfg, tt.ifaceMTU, targets, &errOut)

			if called != tt.wantFactoryCalled {
				t.Errorf("makePinger called = %v, want %v", called, tt.wantFactoryCalled)
			}
			if packetSize != tt.wantPacketSize {
				t.Errorf("packetSize = %d, want %d", packetSize, tt.wantPacketSize)
			}
			if tt.wantNoPreLogs && len(preLogs) != 0 {
				t.Errorf("preLogs = %v, want none", preLogs)
			}
			if tt.wantErrOut != "" && !strings.Contains(errOut.String(), tt.wantErrOut) {
				t.Errorf("errOut = %q, want it to contain %q", errOut.String(), tt.wantErrOut)
			}
			view := targets[0].GetView()
			if view.PMTU != tt.wantPMTU {
				t.Errorf("target PMTU = %d, want %d", view.PMTU, tt.wantPMTU)
			}
			if view.PMTUBottleneckIP != tt.wantBottleneck {
				t.Errorf("target bottleneck IP = %q, want %q", view.PMTUBottleneckIP, tt.wantBottleneck)
			}
		})
	}
}

// The Log pane parses tview tags, so a raw "[PMTU]" prefix would be eaten as
// an (unknown) colour tag. Progress lines must read like every other line.
func TestSetupPMTU_ProgressLinesKeepTheirPrefixAndTimestamp(t *testing.T) {
	makeFn := func(size int) pingerController {
		return &fakePinger{discoverMTU: 1472, discoverLogs: []string{"[PMTU] payload=1471 OK"}}
	}
	var errOut bytes.Buffer
	cfg := config{mtuEnabled: true, packetSize: 56}

	_, preLogs := setupPMTU(makeFn, cfg, 1500, []*stats.TargetStats{stats.NewTargetStats("example.com")}, &errOut)

	if len(preLogs) != 1 {
		t.Fatalf("preLogs = %q, want one line", preLogs)
	}
	want := regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\] \[PMTU\] payload=1471 OK$`)
	if got := plainLogLine(preLogs[0]); !want.MatchString(got) {
		t.Errorf("rendered line = %q, want %v", got, want)
	}
}

func TestSetupPMTU_PerTargetMixedFamilies(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("named v6"), stats.NewTargetStats("IPv4")}
	targets[0].SetResolveAddress("2001:db8::1")
	targets[1].SetResolveAddress("192.0.2.1")
	fake := &fakePinger{discoverPath: func(dest string) (pinger.PathMTUResult, error) {
		switch dest {
		case "2001:db8::1":
			return pinger.PathMTUResult{IP: dest, MTU: 1450, Payload: 1402, Source: "os", Status: "estimated"}, nil
		case "192.0.2.1":
			return pinger.PathMTUResult{IP: dest, MTU: 1500, Payload: 1472, Source: "probe", Status: "probed"}, nil
		default:
			return pinger.PathMTUResult{}, io.ErrUnexpectedEOF
		}
	}}
	var errOut bytes.Buffer
	size, _ := setupPMTU(func(int) pingerController { return fake }, config{mtuEnabled: true, packetSize: 56}, 1500, targets, &errOut)
	if errOut.Len() != 0 || size != 56 {
		t.Fatalf("size=%d error=%s", size, errOut.String())
	}
	v6, v4 := targets[0].GetView(), targets[1].GetView()
	if v6.PathMTU != 1450 || v6.PMTUStatus != "estimated" || v4.PathMTU != 1500 || v4.PMTUStatus != "probed" {
		t.Fatalf("v6=%+v v4=%+v", v6, v4)
	}
	if !fake.isReleased() {
		t.Fatal("discovery pinger was not closed and stopped")
	}
}

func TestSetupPMTU_PerTargetIPv6PartialFailure(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("::1"), stats.NewTargetStats("2001:db8::1"), stats.NewTargetStats("2001:db8::2")}
	fake := &fakePinger{discoverPath: func(dest string) (pinger.PathMTUResult, error) {
		switch dest {
		case "::1":
			return pinger.PathMTUResult{IP: dest, MTU: 16384, Payload: 16336, Source: "os", Status: "confirmed"}, nil
		case "2001:db8::1":
			return pinger.PathMTUResult{IP: dest, MTU: 1450, Payload: 1402, Source: "os", Status: "estimated"}, io.ErrUnexpectedEOF
		default:
			return pinger.PathMTUResult{}, io.ErrUnexpectedEOF
		}
	}}
	var errOut bytes.Buffer
	size, _ := setupPMTU(func(int) pingerController { return fake }, config{mtuEnabled: true, ipv6Only: true, packetSize: 56}, 1500, targets, &errOut)
	if size != 56 || !strings.Contains(errOut.String(), "2001:db8::2") {
		t.Fatalf("size=%d errors=%s", size, errOut.String())
	}
	confirmed, estimated, unknown := targets[0].GetView(), targets[1].GetView(), targets[2].GetView()
	if confirmed.PathMTU != 16384 || confirmed.PMTUStatus != "confirmed" || estimated.PathMTU != 1450 || estimated.PMTUStatus != "estimated" || unknown.PMTU != 0 {
		t.Fatalf("PMTU states were lost or shared: %+v / %+v / %+v", confirmed, estimated, unknown)
	}
}
