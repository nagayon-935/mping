package pinger

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// TestGetPTR_ReturnsName verifies a successful PTR lookup returns the
// resolved hostname with any trailing dot stripped.
func TestGetPTR_ReturnsName(t *testing.T) {
	mockLookup := func(ip string) ([]string, error) {
		if ip == "8.8.8.8" {
			return []string{"dns.google."}, nil
		}
		return nil, fmt.Errorf("not found")
	}
	p := NewPingerWithOptions(nil, Options{PtrEnabled: true, LookupAddr: mockLookup})
	p.ptrJitter = func() time.Duration { return 0 }

	name := p.getPTR("8.8.8.8")
	if name != "dns.google" {
		t.Errorf("expected dns.google, got %q", name)
	}
}

// TestGetPTR_CachesResult verifies a second lookup for the same IP hits the
// cache instead of calling LookupAddr again.
func TestGetPTR_CachesResult(t *testing.T) {
	callCount := 0
	mockLookup := func(ip string) ([]string, error) {
		callCount++
		return []string{"dns.google."}, nil
	}
	p := NewPingerWithOptions(nil, Options{PtrEnabled: true, LookupAddr: mockLookup})
	p.ptrJitter = func() time.Duration { return 0 }

	_ = p.getPTR("8.8.8.8")
	countAfterFirst := callCount
	_ = p.getPTR("8.8.8.8") // should hit cache
	if callCount != countAfterFirst {
		t.Errorf("second call should use cache; want %d calls, got %d", countAfterFirst, callCount)
	}
}

// TestGetPTR_LookupFailure_FallsBackToEmpty verifies a failed lookup (no PTR
// record) returns "" so callers fall back to displaying the plain IP,
// rather than caching the failure (so a later retry isn't permanently
// blocked, mirroring getASNInfo's NA handling).
func TestGetPTR_LookupFailure_FallsBackToEmpty(t *testing.T) {
	callCount := 0
	mockLookup := func(ip string) ([]string, error) {
		callCount++
		return nil, fmt.Errorf("no such host")
	}
	p := NewPingerWithOptions(nil, Options{PtrEnabled: true, LookupAddr: mockLookup})
	p.ptrJitter = func() time.Duration { return 0 }

	name := p.getPTR("1.1.1.1")
	if name != "" {
		t.Errorf("expected empty string on lookup failure, got %q", name)
	}
	_ = p.getPTR("1.1.1.1")
	if callCount != 2 {
		t.Errorf("failures should not be cached; want 2 calls, got %d", callCount)
	}
}

// TestPinger_LookupPTR verifies lookupPTR sets the target's PTR field on a
// successful lookup.
func TestPinger_LookupPTR(t *testing.T) {
	mockLookup := func(ip string) ([]string, error) {
		return []string{"dns.google."}, nil
	}
	target := stats.NewTargetStats("8.8.8.8")
	p := NewPingerWithOptions(nil, Options{PtrEnabled: true, LookupAddr: mockLookup})
	p.ptrJitter = func() time.Duration { return 0 }

	p.lookupPTR(target, "8.8.8.8")
	if target.GetView().PTR != "dns.google" {
		t.Errorf("lookupPTR did not set PTR on target, got %q", target.GetView().PTR)
	}
}

// TestLookupPTR_ReturnsEarlyAfterStop verifies that once p.done is closed,
// lookupPTR must not perform a new DNS lookup — mirrors
// TestLookupASN_ReturnsEarlyAfterStop.
func TestLookupPTR_ReturnsEarlyAfterStop(t *testing.T) {
	called := false
	mockLookup := func(ip string) ([]string, error) {
		called = true
		return []string{"dns.google."}, nil
	}
	target := stats.NewTargetStats("8.8.8.8")
	p := NewPingerWithOptions(nil, Options{PtrEnabled: true, LookupAddr: mockLookup})
	p.ptrJitter = func() time.Duration { return 0 }
	p.Stop()

	p.lookupPTR(target, "8.8.8.8")

	if called {
		t.Error("lookupPTR invoked LookupAddr after Stop(); expected early return")
	}
	if got := target.GetView().PTR; got != "" {
		t.Errorf("PTR should remain unset after Stop(), got %q", got)
	}
}

// TestLookupAddrBoundedUnblocksOnStop verifies an in-flight PTR lookup stops
// blocking as soon as Stop() closes p.done, instead of holding Wait() for
// the full ptrLookupTimeout — mirrors TestLookupTXTBoundedUnblocksOnStop.
func TestLookupAddrBoundedUnblocksOnStop(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	p := NewPingerWithOptions(nil, Options{
		LookupAddr: func(string) ([]string, error) {
			<-release // simulates a DNS server that never answers
			return nil, nil
		},
	})

	returned := make(chan error, 1)
	go func() {
		_, err := p.lookupAddrBounded("8.8.8.8")
		returned <- err
	}()

	// Let the lookup goroutine get into its blocking call.
	time.Sleep(20 * time.Millisecond)
	p.Stop()

	select {
	case err := <-returned:
		if !errors.Is(err, errPingerStopped) {
			t.Fatalf("expected errPingerStopped, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("lookupAddrBounded did not return within 1s of Stop()")
	}
}

// TestLookupAddrBounded_TimesOut verifies a PTR query that never returns
// doesn't hang getPTR forever (mirrors TestLookupTXTBounded_TimesOut).
func TestLookupAddrBounded_TimesOut(t *testing.T) {
	orig := ptrLookupTimeout
	ptrLookupTimeout = 20 * time.Millisecond
	defer func() { ptrLookupTimeout = orig }()

	blocked := make(chan struct{})
	defer close(blocked)
	p := NewPingerWithOptions(nil, Options{
		LookupAddr: func(ip string) ([]string, error) {
			<-blocked
			return []string{"dns.google."}, nil
		},
	})

	names, err := p.lookupAddrBounded("8.8.8.8")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if names != nil {
		t.Errorf("expected nil names on timeout, got %v", names)
	}
}

// TestGetPTR_JitterZero_ReturnsImmediately verifies that with jitter
// disabled (the deterministic test configuration), getPTR doesn't add any
// artificial delay before its first DNS query.
func TestGetPTR_JitterZero_ReturnsImmediately(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		LookupAddr: func(ip string) ([]string, error) {
			return []string{"dns.google."}, nil
		},
	})
	p.ptrJitter = func() time.Duration { return 0 }

	start := time.Now()
	name := p.getPTR("8.8.8.8")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("getPTR took %s with zero jitter, expected near-instant", elapsed)
	}
	if name != "dns.google" {
		t.Errorf("expected dns.google, got %q", name)
	}
}

// TestResolveTarget_PTRLookup_TracksGoroutineInWaitGroup verifies that when
// PtrEnabled is set, resolveTarget's PTR lookup goroutine is added to p.wg
// (so Wait() blocks until the uncancellable DNS lookup finishes), mirroring
// TestResolveTarget_TracksASNGoroutineInWaitGroup for ASN.
func TestResolveTarget_PTRLookup_TracksGoroutineInWaitGroup(t *testing.T) {
	started := make(chan struct{})
	var startOnce sync.Once
	release := make(chan struct{})
	mockLookup := func(ip string) ([]string, error) {
		startOnce.Do(func() { close(started) })
		<-release
		return []string{"dns.google."}, nil
	}
	target := stats.NewTargetStats("8.8.8.8")
	p := NewPingerWithOptions(nil, Options{
		PtrEnabled: true,
		LookupAddr: mockLookup,
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("8.8.8.8")}, nil
		},
	})
	p.ptrJitter = func() time.Duration { return 0 }

	p.resolveTarget(target)

	select {
	case <-started:
	case <-time.After(1 * time.Second):
		t.Fatal("PTR lookup goroutine never started")
	}

	waitDone := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		t.Fatal("wg.Wait() returned before the in-flight PTR lookup finished")
	case <-time.After(50 * time.Millisecond):
		// expected: Wait() is still blocked on the lookup goroutine.
	}

	close(release)

	select {
	case <-waitDone:
		// success: Wait() unblocked once the lookup goroutine finished.
	case <-time.After(1 * time.Second):
		t.Fatal("wg.Wait() did not return after the PTR lookup completed")
	}
}

// TestAnnotateHopIP covers the ASN/PTR annotation helper used by TraceRoute
// to append AS/organization and reverse-DNS info to a hop's responder IP.
func TestAnnotateHopIP(t *testing.T) {
	getASN := func(ip string) ASNInfo { return ASNInfo{Number: "AS15169", Org: "Google LLC"} }
	getPTR := func(ip string) string { return "dns.google" }
	noASN := func(ip string) ASNInfo { return ASNInfo{} }
	noPTR := func(ip string) string { return "" }

	tests := []struct {
		name       string
		ip         string
		asnEnabled bool
		ptrEnabled bool
		getASN     func(string) ASNInfo
		getPTR     func(string) string
		want       string
	}{
		{"star passthrough", "*", true, true, getASN, getPTR, "*"},
		{"empty passthrough", "", true, true, getASN, getPTR, ""},
		{"neither enabled", "8.8.8.8", false, false, getASN, getPTR, "8.8.8.8"},
		{"asn only", "8.8.8.8", true, false, getASN, getPTR, "8.8.8.8(AS15169 Google LLC)"},
		{"ptr only", "8.8.8.8", false, true, getASN, getPTR, "8.8.8.8 dns.google"},
		{"both", "8.8.8.8", true, true, getASN, getPTR, "8.8.8.8(AS15169 Google LLC) dns.google"},
		{"ptr enabled but lookup fails", "8.8.8.8", false, true, noASN, noPTR, "8.8.8.8"},
		{"asn enabled but lookup fails", "8.8.8.8", true, false, noASN, noPTR, "8.8.8.8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := annotateHopIP(tt.ip, tt.asnEnabled, tt.getASN, tt.ptrEnabled, tt.getPTR)
			if got != tt.want {
				t.Errorf("annotateHopIP(%q) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}
