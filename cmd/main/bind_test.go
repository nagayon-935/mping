package main

import (
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
)

// testBind is the -S/-I pair used throughout this file. 192.0.2.5 is TEST-NET-1
// (RFC 5737), so nothing here can accidentally reach a real host.
var testBind = pinger.BindConfig{Source: "192.0.2.5", Interface: "eth0"}

// TestCheckerBindConfig pins the rule that the port and HTTP checkers are
// handed exactly the (source, interface) pair makePingerFactory feeds the ICMP
// pinger via SetSource/SetInterface — the whole point of this wiring is that
// one mping invocation cannot use different egress paths for different
// checks. With neither flag given they must receive a zero BindConfig, which
// leaves their dialling untouched.
func TestCheckerBindConfig(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config
		bindIP string
		want   pinger.BindConfig
	}{
		{"mirrors the pinger binding", config{ifaceName: "eth0"}, "192.0.2.5", pinger.BindConfig{Source: "192.0.2.5", Interface: "eth0"}},
		{"unset flags yield the zero value", config{}, "", pinger.BindConfig{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkerBindConfig(tt.cfg, tt.bindIP)

			if got != tt.want {
				t.Errorf("checkerBindConfig() = %+v, want %+v", got, tt.want)
			}
			if tt.want == (pinger.BindConfig{}) && !got.IsZero() {
				t.Errorf("checkerBindConfig() = %+v, want IsZero()", got)
			}
		})
	}
}

// TestResolverBindConfig covers expandTargets' -S/-I lookup helper: an
// explicit -S is carried straight through without any network I/O
// (determineSourceIPs returns cfg.sourceAddr as-is), unset flags keep
// --resolve-all's DNS lookups unbound, and a -I naming a missing interface
// degrades to an unbound resolver rather than propagating the error — the
// authoritative error for a bad -I surfaces later via prepareRunEnv's own
// determineSourceIPs call, so duplicating that failure here would just add a
// second, redundant error path for the same misconfiguration.
func TestResolverBindConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		want pinger.BindConfig
	}{
		{"explicit -S and -I are carried through", config{sourceAddr: "192.0.2.5", ifaceName: "eth0"}, pinger.BindConfig{Source: "192.0.2.5", Interface: "eth0"}},
		{"unset flags yield the zero value", config{}, pinger.BindConfig{}},
		{"resolution error falls back to the zero value", config{ifaceName: "no-such-iface-xyz-mping-test"}, pinger.BindConfig{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolverBindConfig(tt.cfg, nil); got != tt.want {
				t.Errorf("resolverBindConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSetupHTTPChecker_NoURLs(t *testing.T) {
	sup := newSupervisor(supervisorConfig{interval: time.Second, timeout: time.Second})
	if hc := sup.setupHTTPChecker(); hc != nil {
		t.Error("expected nil HTTPChecker for empty URL list")
	}
}

// newBindTestSupervisor builds a supervisor that owns both a port checker and
// an HTTP checker, configured with testBind and intervals long enough that no
// periodic check fires during the test.
func newBindTestSupervisor() *supervisor {
	return newSupervisor(supervisorConfig{
		makePinger: func(size int) pingerController { return &fakePinger{} },
		targets:    []*stats.TargetStats{stats.NewTargetStats("example.com")},
		interval:   time.Hour,
		timeout:    100 * time.Millisecond,
		portSpecs:  []pinger.PortSpec{{Port: 443, Protocol: "tcp"}},
		httpURLs:   []string{"http://127.0.0.1:1"},
		bind:       testBind,
		logCh:      make(chan string, 8),
	})
}

// TestSupervisor_StartAllPassesBindConfigToCheckers verifies the supervisor
// forwards its configured binding when it first brings the checkers up
// (through setupPortChecker / setupHTTPChecker).
func TestSupervisor_StartAllPassesBindConfigToCheckers(t *testing.T) {
	sup := newBindTestSupervisor()
	if err := sup.handle(command{kind: cmdStart}); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	defer sup.tearDownAll()

	if got := sup.portChecker.BindConfig(); got != testBind {
		t.Errorf("portChecker.BindConfig() = %+v, want %+v", got, testBind)
	}
	if got := sup.httpChecker.BindConfig(); got != testBind {
		t.Errorf("httpChecker.BindConfig() = %+v, want %+v", got, testBind)
	}
}

// TestSupervisor_RestartCheckersPassBindConfig covers the reset paths ('p' and
// 'h' in the TUI), which build brand-new checkers and would otherwise be able
// to drop the binding the initial start applied.
func TestSupervisor_RestartCheckersPassBindConfig(t *testing.T) {
	sup := newBindTestSupervisor()
	if err := sup.handle(command{kind: cmdStart}); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	defer sup.tearDownAll()

	sup.restartPortChecker()
	if got := sup.portChecker.BindConfig(); got != testBind {
		t.Errorf("after restartPortChecker: BindConfig() = %+v, want %+v", got, testBind)
	}

	sup.restartHTTPChecker()
	if got := sup.httpChecker.BindConfig(); got != testBind {
		t.Errorf("after restartHTTPChecker: BindConfig() = %+v, want %+v", got, testBind)
	}
}
