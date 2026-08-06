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

// TestCheckerBindConfig_MirrorsPingerBinding pins the rule that the port and
// HTTP checkers are handed exactly the (source, interface) pair
// makePingerFactory feeds the ICMP pinger via SetSource/SetInterface — the
// whole point of this wiring is that one mping invocation cannot use different
// egress paths for different checks.
func TestCheckerBindConfig_MirrorsPingerBinding(t *testing.T) {
	cfg := config{ifaceName: "eth0"}

	got := checkerBindConfig(cfg, "192.0.2.5")

	if got.Source != "192.0.2.5" {
		t.Errorf("Source: got %q, want %q", got.Source, "192.0.2.5")
	}
	if got.Interface != "eth0" {
		t.Errorf("Interface: got %q, want %q", got.Interface, "eth0")
	}
}

// TestCheckerBindConfig_UnsetFlagsYieldZeroValue keeps the fallback explicit:
// with neither flag given the checkers must receive a zero BindConfig, which
// leaves their dialling untouched.
func TestCheckerBindConfig_UnsetFlagsYieldZeroValue(t *testing.T) {
	if got := checkerBindConfig(config{}, ""); !got.IsZero() {
		t.Errorf("checkerBindConfig: got %+v, want zero value", got)
	}
}

// ---- setup* helpers ----

func TestSetupPortChecker_PassesBindConfig(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("example.com")}
	specs := []pinger.PortSpec{{Port: 443, Protocol: "tcp"}}

	pc := setupPortChecker(targets, specs, time.Hour, 100*time.Millisecond, testBind)
	if pc == nil {
		t.Fatal("expected non-nil PortChecker for a non-empty spec list")
	}
	defer func() {
		pc.Stop()
		pc.Wait()
	}()

	if got := pc.BindConfig(); got != testBind {
		t.Errorf("PortChecker.BindConfig() = %+v, want %+v", got, testBind)
	}
}

func TestSetupHTTPChecker_PassesBindConfig(t *testing.T) {
	hc := setupHTTPChecker([]string{"http://127.0.0.1:1"}, time.Hour, 100*time.Millisecond, testBind)
	if hc == nil {
		t.Fatal("expected non-nil HTTPChecker for a non-empty URL list")
	}
	defer func() {
		hc.Stop()
		hc.Wait()
	}()

	if got := hc.BindConfig(); got != testBind {
		t.Errorf("HTTPChecker.BindConfig() = %+v, want %+v", got, testBind)
	}
}

// ---- supervisor ----

// newBindTestSupervisor builds a supervisor that owns both a port checker and
// an HTTP checker, configured with testBind and intervals long enough that no
// periodic check fires during the test.
func newBindTestSupervisor() *supervisor {
	return newSupervisor(supervisorConfig{
		makePinger: func(size int) pingerController { return &lifecycleFakePinger{} },
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
// forwards its configured binding when it first brings the checkers up.
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
