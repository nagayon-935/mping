package pinger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type fakePacketConnV4 struct {
	PacketConnV4
}

func (f *fakePacketConnV4) Close() error { return nil }
func (f *fakePacketConnV4) WriteTo(b []byte, cm *ipv4.ControlMessage, dst net.Addr) (int, error) {
	return len(b), nil
}

func TestPinger_ASNLookupDirect(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		if strings.Contains(query, "8.8.8.8") || strings.Contains(query, "15169.asn") {
			if strings.Contains(query, "origin") {
				return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
			}
			return []string{"15169 | GOOGLE - Google LLC, US | 1992-12-01"}, nil
		}
		if strings.Contains(query, "1.1.1.1") {
			return []string{"NA | 1.1.1.0/24 | US | arin | 1992-12-01"}, nil
		}
		if strings.Contains(query, "origin6") {
			return []string{"15169 | 2001:4860:4860::/48 | US | arin | 2005-01-01"}, nil
		}
		return nil, fmt.Errorf("not found")
	}

	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
	})
	p.asnJitter = func() time.Duration { return 0 }

	// Test IPv4 lookup — backward-compat string method
	asn := p.getASN("8.8.8.8")
	if asn != "AS15169" {
		t.Errorf("expected AS15169, got %q", asn)
	}

	// Test cache
	asn = p.getASN("8.8.8.8")
	if asn != "AS15169" {
		t.Errorf("expected cached AS15169, got %q", asn)
	}

	// Test NA
	asn = p.getASN("1.1.1.1")
	if asn != "" {
		t.Errorf("expected empty string for NA, got %q", asn)
	}

	// Test empty/invalid
	if p.getASN("") != "" {
		t.Error("expected empty string for empty IP")
	}
	if p.getASN("invalid") != "" {
		t.Error("expected empty string for invalid IP")
	}

	// Test error from LookupTXT
	if p.getASN("0.0.0.0") != "" {
		t.Error("expected empty string on lookup error")
	}
}

func TestGetASNInfo_ExtractsCountryAndOrg(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		switch {
		case strings.Contains(query, "origin"):
			return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
		case strings.Contains(query, "15169.asn"):
			return []string{"15169 | GOOGLE - Google LLC, US | 1992-12-01"}, nil
		}
		return nil, fmt.Errorf("not found")
	}
	p := NewPingerWithOptions(nil, Options{AsnEnabled: true, LookupTXT: mockLookup})
	p.asnJitter = func() time.Duration { return 0 }

	info := p.getASNInfo("8.8.8.8")
	if info.Number != "AS15169" {
		t.Errorf("Number: want AS15169, got %q", info.Number)
	}
	if info.Country != "US" {
		t.Errorf("Country: want US, got %q", info.Country)
	}
	if info.Org != "Google LLC" {
		t.Errorf("Org: want Google LLC, got %q", info.Org)
	}
}

func TestGetASNInfo_OrgLookupFails_StillReturnsCountry(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		if strings.Contains(query, "origin") {
			return []string{"15169 | 8.8.8.0/24 | JP | arin | 1992-12-01"}, nil
		}
		return nil, fmt.Errorf("asn lookup fail")
	}
	p := NewPingerWithOptions(nil, Options{AsnEnabled: true, LookupTXT: mockLookup})
	p.asnJitter = func() time.Duration { return 0 }

	info := p.getASNInfo("8.8.8.8")
	if info.Number != "AS15169" {
		t.Errorf("Number: want AS15169, got %q", info.Number)
	}
	if info.Country != "JP" {
		t.Errorf("Country: want JP, got %q", info.Country)
	}
	if info.Org != "" {
		t.Errorf("Org: want empty on lookup failure, got %q", info.Org)
	}
}

func TestGetASNInfo_CachesFullInfo(t *testing.T) {
	callCount := 0
	mockLookup := func(query string) ([]string, error) {
		callCount++
		if strings.Contains(query, "origin") {
			return []string{"64512 | 10.0.0.0/8 | DE | ripe | 2000-01-01"}, nil
		}
		return []string{"64512 | EXAMPLE - Example Corp, DE | 2000-01-01"}, nil
	}
	p := NewPingerWithOptions(nil, Options{AsnEnabled: true, LookupTXT: mockLookup})
	p.asnJitter = func() time.Duration { return 0 }

	_ = p.getASNInfo("10.0.0.1")
	countAfterFirst := callCount
	_ = p.getASNInfo("10.0.0.1") // should hit cache
	if callCount != countAfterFirst {
		t.Errorf("second call should use cache; want %d calls, got %d", countAfterFirst, callCount)
	}
}

func TestGetASNInfo_NA_ReturnsEmpty(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		return []string{"NA | 1.1.1.0/24 | US | arin | 1992-12-01"}, nil
	}
	p := NewPingerWithOptions(nil, Options{AsnEnabled: true, LookupTXT: mockLookup})
	p.asnJitter = func() time.Duration { return 0 }

	info := p.getASNInfo("1.1.1.1")
	if info.Number != "" || info.Country != "" || info.Org != "" {
		t.Errorf("NA response should return empty ASNInfo, got %+v", info)
	}
}

func TestPinger_LookupASN(t *testing.T) {
	mockLookup := func(query string) ([]string, error) {
		return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
	}
	target := stats.NewTargetStats("8.8.8.8")
	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
	})
	p.asnJitter = func() time.Duration { return 0 }
	p.lookupASN(target, "8.8.8.8")
	if target.GetView().ASN != "AS15169" {
		t.Errorf("lookupASN did not set ASN on target")
	}
}

type traceFakePacketConn struct {
	net.PacketConn
}

func (f *traceFakePacketConn) Close() error                      { return nil }
func (f *traceFakePacketConn) SetReadDeadline(t time.Time) error { return nil }
func (f *traceFakePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	return 0, nil, fmt.Errorf("timeout")
}
func (f *traceFakePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	return len(b), nil
}
func (f *traceFakePacketConn) Read(b []byte) (int, error)         { return 0, fmt.Errorf("not implemented") }
func (f *traceFakePacketConn) Write(b []byte) (int, error)        { return len(b), nil }
func (f *traceFakePacketConn) LocalAddr() net.Addr                { return &net.IPAddr{IP: net.IPv4zero} }
func (f *traceFakePacketConn) RemoteAddr() net.Addr               { return &net.IPAddr{IP: net.IPv4zero} }
func (f *traceFakePacketConn) SetDeadline(t time.Time) error      { return nil }
func (f *traceFakePacketConn) SetWriteDeadline(t time.Time) error { return nil }

func TestPinger_TraceRoute_AsnEnabled(t *testing.T) {
	t.Skip("Flaky in test environment - traceCh registration timing issue")
	mockLookup := func(query string) ([]string, error) {
		return []string{"12345 | 1.1.1.0/24 | US | arin | 1992-12-01"}, nil
	}

	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("1.1.1.1").To4()}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &traceFakePacketConn{}, nil
		},
	})
	p.Source = "127.0.0.1"

	p.connV4 = &fakePacketConnV4{}

	done := make(chan struct{})
	var hops []string
	var err error

	go func() {
		hops, err = p.TraceRoute(context.Background(), "1.1.1.1", 1, 200*time.Millisecond)
		close(done)
	}()

	var traceCh chan traceMsg
	start := time.Now()
	for time.Since(start) < 2*time.Second {
		p.traceChansMu.RLock()
		if len(p.traceChans) > 0 {
			for _, val := range p.traceChans {
				traceCh = val
				break
			}
		}
		p.traceChansMu.RUnlock()
		if traceCh != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
		select {
		case <-done:
			if err != nil {
				t.Fatalf("TraceRoute exited early with error: %v", err)
			}
		default:
		}
	}

	if traceCh == nil {
		t.Fatal("traceCh not registered")
	}

	time.Sleep(50 * time.Millisecond)

	baseID := p.baseID
	traceID := (baseID + 0x1234 + 1) & 0xffff

	innerIP := make([]byte, 28)
	innerIP[0] = 0x45
	innerIP[9] = 1
	innerIP[20] = 8
	innerIP[24] = byte(traceID >> 8)
	innerIP[25] = byte(traceID & 0xff)
	innerIP[26] = byte(1 >> 8)
	innerIP[27] = byte(1 & 0xff)

	traceCh <- traceMsg{
		parsed: &icmp.Message{
			Type: ipv4.ICMPTypeTimeExceeded,
			Body: &icmp.TimeExceeded{
				Data: innerIP,
			},
		},
		src: &net.IPAddr{IP: net.ParseIP("1.1.1.1")},
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("TraceRoute timed out")
	}

	if err != nil {
		t.Fatalf("TraceRoute error: %v", err)
	}

	if len(hops) == 0 {
		t.Fatal("expected 1 hop")
	}

	if !strings.Contains(hops[0], "AS12345") {
		t.Errorf("expected ASN in hop output, got %q", hops[0])
	}
}

// TestResolveTarget_TracksASNGoroutineInWaitGroup verifies the F2 fix:
// resolveTarget's ASN lookup goroutine is added to p.wg, so Wait() blocks
// until the (slow, uncancellable) DNS lookup finishes rather than letting
// it outlive the pinger.
func TestResolveTarget_TracksASNGoroutineInWaitGroup(t *testing.T) {
	started := make(chan struct{})
	var startOnce sync.Once
	release := make(chan struct{})
	mockLookup := func(query string) ([]string, error) {
		startOnce.Do(func() { close(started) })
		<-release
		return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
	}
	target := stats.NewTargetStats("8.8.8.8")
	p := NewPingerWithOptions(nil, Options{
		AsnEnabled: true,
		LookupTXT:  mockLookup,
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("8.8.8.8")}, nil
		},
	})
	p.asnJitter = func() time.Duration { return 0 }

	p.resolveTarget(target)

	select {
	case <-started:
	case <-time.After(1 * time.Second):
		t.Fatal("ASN lookup goroutine never started")
	}

	waitDone := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		t.Fatal("wg.Wait() returned before the in-flight ASN lookup finished")
	case <-time.After(50 * time.Millisecond):
		// expected: Wait() is still blocked on the lookup goroutine.
	}

	close(release)

	select {
	case <-waitDone:
		// success: Wait() unblocked once the lookup goroutine finished.
	case <-time.After(1 * time.Second):
		t.Fatal("wg.Wait() did not return after the ASN lookup completed")
	}
}

// TestLookupASN_ReturnsEarlyAfterStop verifies the F2 fix's early-exit guard:
// once p.done is closed, lookupASN must not perform a new DNS lookup.
func TestLookupASN_ReturnsEarlyAfterStop(t *testing.T) {
	called := false
	mockLookup := func(query string) ([]string, error) {
		called = true
		return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
	}
	target := stats.NewTargetStats("8.8.8.8")
	p := NewPingerWithOptions(nil, Options{AsnEnabled: true, LookupTXT: mockLookup})
	p.asnJitter = func() time.Duration { return 0 }
	p.Stop()

	p.lookupASN(target, "8.8.8.8")

	if called {
		t.Error("lookupASN invoked LookupTXT after Stop(); expected early return")
	}
	if got := target.GetView().ASN; got != "" {
		t.Errorf("ASN should remain unset after Stop(), got %q", got)
	}
}

// TestLookupTXTBoundedUnblocksOnStop verifies an in-flight Cymru lookup
// stops blocking as soon as Stop() closes p.done, instead of holding
// Wait() for the full asnLookupTimeout. resolveTarget registers ASN lookup
// goroutines on p.wg, so without this the shutdown path (supervisor's
// cur.Wait()) stalls for up to two lookup timeouts.
func TestLookupTXTBoundedUnblocksOnStop(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	p := NewPingerWithOptions(nil, Options{
		LookupTXT: func(string) ([]string, error) {
			<-release // simulates a DNS server that never answers
			return nil, nil
		},
	})

	returned := make(chan error, 1)
	go func() {
		_, err := p.lookupTXTBounded("1.0.0.10.origin.asn.cymru.com")
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
		t.Fatal("lookupTXTBounded did not return within 1s of Stop()")
	}
}

// TestGetASNInfo_StopDuringOrgLookupDoesNotCache verifies that when Stop()
// closes p.done while the second (org) Cymru query is in flight, getASNInfo
// discards the result instead of caching a partial record: a real ASN/
// country from the completed origin query paired with a bogus empty Org
// that would be indistinguishable from a genuine "no org found" response.
func TestGetASNInfo_StopDuringOrgLookupDoesNotCache(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	mockLookup := func(query string) ([]string, error) {
		if strings.Contains(query, "origin") {
			return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
		}
		// org lookup: simulate a DNS server that never answers, so it is
		// still in flight when Stop() fires below.
		<-release
		return []string{"15169 | GOOGLE - Google LLC, US | 1992-12-01"}, nil
	}

	p := NewPingerWithOptions(nil, Options{LookupTXT: mockLookup})
	p.asnJitter = func() time.Duration { return 0 }

	returned := make(chan ASNInfo, 1)
	go func() {
		returned <- p.getASNInfo("8.8.8.8")
	}()

	// Let the origin lookup complete and the org lookup get into its
	// blocking call.
	time.Sleep(20 * time.Millisecond)
	p.Stop()

	select {
	case info := <-returned:
		if info != (ASNInfo{}) {
			t.Fatalf("expected zero-value ASNInfo after Stop() mid-org-lookup, got %+v", info)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("getASNInfo did not return within 1s of Stop()")
	}

	p.asnMu.RLock()
	_, cached := p.asnCache["8.8.8.8"]
	p.asnMu.RUnlock()
	if cached {
		t.Error("expected no cache entry for 8.8.8.8 after Stop() aborted the org lookup")
	}
}

// TestLookupTXTBounded_TimesOut verifies that a Cymru DNS query that never
// returns doesn't hang getASNInfo forever (the default net/Resolver-based
// lookupTXT has no timeout of its own).
func TestLookupTXTBounded_TimesOut(t *testing.T) {
	orig := asnLookupTimeout
	asnLookupTimeout = 20 * time.Millisecond
	defer func() { asnLookupTimeout = orig }()

	blocked := make(chan struct{})
	defer close(blocked)
	p := NewPingerWithOptions(nil, Options{
		LookupTXT: func(query string) ([]string, error) {
			<-blocked
			return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
		},
	})

	txts, err := p.lookupTXTBounded("8.8.8.8.origin.asn.cymru.com")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if txts != nil {
		t.Errorf("expected nil txts on timeout, got %v", txts)
	}
}

// TestGetASNInfo_JitterZero_ReturnsImmediately verifies that with jitter
// disabled (the deterministic test configuration), getASNInfo doesn't add
// any artificial delay before its first DNS query.
func TestGetASNInfo_JitterZero_ReturnsImmediately(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		LookupTXT: func(query string) ([]string, error) {
			return []string{"15169 | 8.8.8.0/24 | US | arin | 1992-12-01"}, nil
		},
	})
	p.asnJitter = func() time.Duration { return 0 }

	start := time.Now()
	info := p.getASNInfo("8.8.8.8")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("getASNInfo took %s with zero jitter, expected near-instant", elapsed)
	}
	if info.Number != "AS15169" {
		t.Errorf("Number: want AS15169, got %q", info.Number)
	}
}

func TestPinger_TraceRoute_MaxHops(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &traceFakePacketConn{}, nil
		},
	})
	p.connV4 = &fakePacketConnV4{}

	hops, err := p.TraceRoute(context.Background(), "1.1.1.1", 2, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("TraceRoute error: %v", err)
	}
	if len(hops) != 2 || hops[0] != "*" || hops[1] != "*" {
		t.Errorf("expected 2 stars, got %v", hops)
	}
}
