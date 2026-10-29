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
// Interface is a documented no-op in bindToInterface) and keeping -S-only
// runs behaviorally identical to before this feature existed.
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

			factory := makePingerFactory(nil, pinger.Options{}, tt.cfg, tt.bindIP, nil)
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
			name: "IPv6-only skips discovery with a warning",
			cfg:  config{mtuEnabled: true, ipv6Only: true, packetSize: 56}, ifaceMTU: 1500,
			wantPacketSize: 56, wantErrOut: "PMTU discovery disabled", wantNoPreLogs: true,
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

			packetSize, preLogs := setupPMTU(makeFn, tt.cfg, tt.ifaceMTU, targets, "example.com", &errOut)

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

	_, preLogs := setupPMTU(makeFn, cfg, 1500, nil, "example.com", &errOut)

	if len(preLogs) != 1 {
		t.Fatalf("preLogs = %q, want one line", preLogs)
	}
	want := regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\] \[PMTU\] payload=1471 OK$`)
	if got := plainLogLine(preLogs[0]); !want.MatchString(got) {
		t.Errorf("rendered line = %q, want %v", got, want)
	}
}
