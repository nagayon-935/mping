package pinger

import (
	"net"
	"testing"

	"github.com/nagayon-935/mping/internal/stats"
)

// ---- Options -> Pinger DSCP translation ----

func TestNewPingerWithOptionsDSCPUnset(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{})
	if p.DSCP != dscpUnset {
		t.Fatalf("DSCP = %d, want dscpUnset (%d) when Options.DSCP is nil", p.DSCP, dscpUnset)
	}
}

func TestNewPingerWithOptionsDSCPExplicitZero(t *testing.T) {
	// A *int pointing at 0 must set DSCP to CS0 (0), not fall back to
	// dscpUnset — this is exactly the ambiguity Options.DSCP being a
	// pointer (rather than a plain int) exists to avoid.
	zero := 0
	p := NewPingerWithOptions(nil, Options{DSCP: &zero})
	if p.DSCP != 0 {
		t.Fatalf("DSCP = %d, want 0 (explicit CS0)", p.DSCP)
	}
}

func TestNewPingerWithOptionsDSCPSet(t *testing.T) {
	ef := 46 << 2 // EF's TOS byte
	p := NewPingerWithOptions(nil, Options{DSCP: &ef})
	if p.DSCP != ef {
		t.Fatalf("DSCP = %d, want %d", p.DSCP, ef)
	}
}

func TestNewPingerWithOptionsTargetDSCP(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	p := NewPingerWithOptions([]*stats.TargetStats{target}, Options{TargetDSCP: map[int]int{0: 46 << 2}})
	if p.TargetDSCP[target] != 46<<2 {
		t.Fatalf("TargetDSCP not threaded through from Options: got %v", p.TargetDSCP)
	}
}

// ---- armDSCP ----

func TestArmDSCPUnsetIsNoop(t *testing.T) {
	p := NewPinger(nil)
	fv4 := &fakePacketConn{}
	fv6 := &fakePacketConnV6{}
	p.connV4 = fv4
	p.connV6 = fv6
	p.armDSCP() // p.DSCP == dscpUnset by default
	if len(fv4.tosCalls) != 0 {
		t.Fatalf("expected no SetTOS calls when DSCP unset, got %v", fv4.tosCalls)
	}
	if len(fv6.tosCalls) != 0 {
		t.Fatalf("expected no SetTrafficClass calls when DSCP unset, got %v", fv6.tosCalls)
	}
}

func TestArmDSCPSetsBothSockets(t *testing.T) {
	p := NewPinger(nil)
	fv4 := &fakePacketConn{}
	fv6 := &fakePacketConnV6{}
	p.connV4 = fv4
	p.connV6 = fv6
	p.DSCP = 46 << 2
	p.armDSCP()
	if len(fv4.tosCalls) != 1 || fv4.tosCalls[0] != 46<<2 {
		t.Fatalf("fv4.tosCalls = %v, want [%d]", fv4.tosCalls, 46<<2)
	}
	if len(fv6.tosCalls) != 1 || fv6.tosCalls[0] != 46<<2 {
		t.Fatalf("fv6.tosCalls = %v, want [%d]", fv6.tosCalls, 46<<2)
	}
}

func TestArmDSCPSkipsNilConns(t *testing.T) {
	p := NewPinger(nil)
	p.DSCP = 46 << 2
	// Neither connV4 nor connV6 assigned; must not panic.
	p.armDSCP()
}

// ---- dscpFor ----

func TestDSCPForNoOverride(t *testing.T) {
	p := NewPinger(nil)
	target := stats.NewTargetStats("example.com")
	if _, ok := p.dscpFor(target); ok {
		t.Fatal("expected ok=false when TargetDSCP is nil")
	}
}

func TestDSCPForWithOverride(t *testing.T) {
	p := NewPinger(nil)
	target := stats.NewTargetStats("example.com")
	p.TargetDSCP = map[*stats.TargetStats]int{target: 46 << 2}
	got, ok := p.dscpFor(target)
	if !ok {
		t.Fatal("expected ok=true for overridden target")
	}
	if got != 46<<2 {
		t.Fatalf("dscpFor = %d, want %d", got, 46<<2)
	}
}

func TestDSCPForOtherTargetUnaffected(t *testing.T) {
	// Regression guard for the "EF vs BE side-by-side" use case: a
	// TargetDSCP override for one host must never leak onto another.
	p := NewPinger(nil)
	p.TargetDSCP = map[*stats.TargetStats]int{stats.NewTargetStats("ef.example.com"): 46 << 2}
	other := stats.NewTargetStats("be.example.com")
	if _, ok := p.dscpFor(other); ok {
		t.Fatal("expected ok=false for a target with no override, even when TargetDSCP has entries for other hosts")
	}
}

// ---- getWriteFunc: per-target DSCP attaches an IPv6 ControlMessage ----

func TestGetWriteFuncIPv6AttachesControlMessageWhenOverridden(t *testing.T) {
	p := NewPinger(nil)
	fake := &fakePacketConnV6{}
	p.connV6 = fake
	_, writeFunc, errStr := p.getWriteFunc(&net.IPAddr{IP: net.ParseIP("2001:db8::1")}, 46<<2, true)
	if errStr != "" {
		t.Fatalf("errStr = %q, want empty", errStr)
	}
	if _, err := writeFunc([]byte("payload"), &net.IPAddr{IP: net.ParseIP("2001:db8::1")}); err != nil {
		t.Fatalf("writeFunc: %v", err)
	}
	if len(fake.writeCMs) != 1 || fake.writeCMs[0] == nil {
		t.Fatalf("expected a non-nil ControlMessage attached to WriteTo, got %v", fake.writeCMs)
	}
	if fake.writeCMs[0].TrafficClass != 46<<2 {
		t.Fatalf("ControlMessage.TrafficClass = %d, want %d", fake.writeCMs[0].TrafficClass, 46<<2)
	}
}

func TestGetWriteFuncIPv6NoControlMessageWithoutOverride(t *testing.T) {
	p := NewPinger(nil)
	fake := &fakePacketConnV6{}
	p.connV6 = fake
	_, writeFunc, _ := p.getWriteFunc(&net.IPAddr{IP: net.ParseIP("2001:db8::1")}, dscpUnset, false)
	if _, err := writeFunc([]byte("payload"), &net.IPAddr{IP: net.ParseIP("2001:db8::1")}); err != nil {
		t.Fatalf("writeFunc: %v", err)
	}
	if len(fake.writeCMs) != 1 || fake.writeCMs[0] != nil {
		t.Fatalf("expected a nil ControlMessage (fall back to socket-wide default) when not overridden, got %v", fake.writeCMs)
	}
}

// TestGetWriteFuncIPv4IgnoresPerTargetDSCP documents (and locks in) the
// library-level limitation described on PacketConnV4.SetTOS: x/net's
// ipv4.ControlMessage has no TOS field, so IPv4 has no per-packet write-side
// hook — a per-target override is silently unusable for an IPv4 target, and
// WriteTo is always called with a nil ControlMessage.
func TestGetWriteFuncIPv4IgnoresPerTargetDSCP(t *testing.T) {
	p := NewPinger(nil)
	p.connV4 = &fakePacketConn{}
	_, writeFunc, errStr := p.getWriteFunc(&net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}, 46<<2, true)
	if errStr != "" {
		t.Fatalf("errStr = %q, want empty", errStr)
	}
	if writeFunc == nil {
		t.Fatal("expected non-nil writeFunc")
	}
	// No panic and no way to observe the DSCP value on the write path for
	// IPv4 (fakePacketConn.WriteTo doesn't even record its cm argument,
	// unlike fakePacketConnV6) — reaching here without error is the
	// assertion.
	if _, err := writeFunc([]byte("payload"), &net.IPAddr{IP: net.IPv4(8, 8, 8, 8)}); err != nil {
		t.Fatalf("writeFunc: %v", err)
	}
}

// ---- sendProbe: dscpFor result reaches getWriteFunc ----

func TestSendProbeUsesPerTargetDSCPOverIPv6(t *testing.T) {
	target := stats.NewTargetStats("ef.example.com")
	p := NewPinger([]*stats.TargetStats{target})
	fake := &fakePacketConnV6{}
	p.connV6 = fake
	p.TargetDSCP = map[*stats.TargetStats]int{target: 46 << 2}

	dst := &net.IPAddr{IP: net.ParseIP("2001:db8::1")}
	if _, ok := p.sendProbe(target, 1, 1, []byte("MPING"), dst); !ok {
		t.Fatal("sendProbe reported failure")
	}
	if len(fake.writeCMs) != 1 || fake.writeCMs[0] == nil || fake.writeCMs[0].TrafficClass != 46<<2 {
		t.Fatalf("expected WriteTo's ControlMessage.TrafficClass = %d, got %v", 46<<2, fake.writeCMs)
	}
}

func TestSendProbeNoOverrideLeavesControlMessageNil(t *testing.T) {
	target := stats.NewTargetStats("be.example.com")
	p := NewPinger([]*stats.TargetStats{target})
	fake := &fakePacketConnV6{}
	p.connV6 = fake
	// No TargetDSCP entry for this host, and no global p.DSCP either.

	dst := &net.IPAddr{IP: net.ParseIP("2001:db8::1")}
	if _, ok := p.sendProbe(target, 1, 1, []byte("MPING"), dst); !ok {
		t.Fatal("sendProbe reported failure")
	}
	if len(fake.writeCMs) != 1 || fake.writeCMs[0] != nil {
		t.Fatalf("expected nil ControlMessage (defer to socket-wide default), got %v", fake.writeCMs)
	}
}

func TestSameHostDSCPUsesDifferentIPv6Packets(t *testing.T) {
	targets := []*stats.TargetStats{stats.NewTargetStats("::1"), stats.NewTargetStats("::1"), stats.NewTargetStats("::1")}
	p := NewPingerWithOptions(targets, Options{TargetDSCP: map[int]int{0: 46 << 2, 1: 0}})
	conn := &fakePacketConnV6{}
	p.connV6 = conn
	for i, target := range targets {
		if _, ok := p.sendProbe(target, i+1, 1, []byte("MPING"), &net.IPAddr{IP: net.ParseIP("::1")}); !ok {
			t.Fatal("send failed")
		}
	}
	if len(conn.writeCMs) != 3 || conn.writeCMs[0] == nil || conn.writeCMs[0].TrafficClass != 46<<2 || conn.writeCMs[1] == nil || conn.writeCMs[1].TrafficClass != 0 || conn.writeCMs[2] != nil {
		t.Fatalf("same-host DSCP overrides mixed: %+v", conn.writeCMs)
	}
}
