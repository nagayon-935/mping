package pinger

import (
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

// TestICMPv6FilterFromConfig_AcceptsReceiverV6Types proves the kernel filter
// derived from receiverV6Config passes exactly echoReply + errorTypes, and
// blocks unrelated ICMPv6 control traffic (NS/NA/RA/MLD) that shares the
// same raw protocol number.
func TestICMPv6FilterFromConfig_AcceptsReceiverV6Types(t *testing.T) {
	filt := icmpv6FilterFromConfig(receiverV6Config)

	mustAccept := []ipv6.ICMPType{
		ipv6.ICMPTypeEchoReply,
		ipv6.ICMPTypeDestinationUnreachable,
		ipv6.ICMPTypePacketTooBig,
		ipv6.ICMPTypeTimeExceeded,
		ipv6.ICMPTypeParameterProblem,
	}
	for _, typ := range mustAccept {
		if filt.WillBlock(typ) {
			t.Errorf("expected filter to accept %v (receiverV6Config type), but it blocks it", typ)
		}
	}

	mustBlock := []ipv6.ICMPType{
		ipv6.ICMPTypeEchoRequest,
		ipv6.ICMPTypeMulticastListenerQuery,
		ipv6.ICMPTypeMulticastListenerReport,
		ipv6.ICMPTypeMulticastListenerDone,
		ipv6.ICMPTypeRouterSolicitation,
		ipv6.ICMPTypeRouterAdvertisement,
		ipv6.ICMPTypeNeighborSolicitation,
		ipv6.ICMPTypeNeighborAdvertisement,
		ipv6.ICMPTypeRedirect,
	}
	for _, typ := range mustBlock {
		if !filt.WillBlock(typ) {
			t.Errorf("expected filter to block %v (unrelated L2/control traffic), but it accepts it", typ)
		}
	}
}

// TestICMPv6FilterFromConfig_DerivesFromCfg is the key proof that the filter
// is *derived* from receiverConfig rather than duplicated as a second,
// independently-maintained literal list: feeding a synthetic config with a
// type receiverV6Config does not normally carry (Redirect) makes the filter
// pass Redirect and continue blocking everything receiverV6Config would
// have passed but this synthetic config omits. If the filter were a
// hardcoded copy of receiverV6Config's types, this table would fail.
func TestICMPv6FilterFromConfig_DerivesFromCfg(t *testing.T) {
	tests := []struct {
		name       string
		cfg        receiverConfig
		wantAccept []ipv6.ICMPType
		wantBlock  []ipv6.ICMPType
	}{
		{
			name: "single error type, no overlap with receiverV6Config",
			cfg: receiverConfig{
				echoReply:  ipv6.ICMPTypeEchoReply,
				errorTypes: []icmp.Type{ipv6.ICMPTypeRedirect},
			},
			wantAccept: []ipv6.ICMPType{ipv6.ICMPTypeEchoReply, ipv6.ICMPTypeRedirect},
			// receiverV6Config's own error types must NOT leak in here if the
			// filter were built from a hardcoded second list instead of cfg.
			wantBlock: []ipv6.ICMPType{
				ipv6.ICMPTypeDestinationUnreachable,
				ipv6.ICMPTypePacketTooBig,
				ipv6.ICMPTypeTimeExceeded,
				ipv6.ICMPTypeParameterProblem,
			},
		},
		{
			name: "empty error types accepts only echoReply",
			cfg: receiverConfig{
				echoReply:  ipv6.ICMPTypeEchoReply,
				errorTypes: nil,
			},
			wantAccept: []ipv6.ICMPType{ipv6.ICMPTypeEchoReply},
			wantBlock: []ipv6.ICMPType{
				ipv6.ICMPTypeDestinationUnreachable,
				ipv6.ICMPTypeTimeExceeded,
			},
		},
		{
			name: "growing errorTypes automatically grows the filter",
			cfg: receiverConfig{
				echoReply: ipv6.ICMPTypeEchoReply,
				errorTypes: []icmp.Type{
					ipv6.ICMPTypeDestinationUnreachable,
					ipv6.ICMPTypePacketTooBig,
					ipv6.ICMPTypeTimeExceeded,
					ipv6.ICMPTypeParameterProblem,
					ipv6.ICMPTypeRedirect, // simulates a future addition to errorTypes
				},
			},
			wantAccept: []ipv6.ICMPType{
				ipv6.ICMPTypeEchoReply,
				ipv6.ICMPTypeDestinationUnreachable,
				ipv6.ICMPTypePacketTooBig,
				ipv6.ICMPTypeTimeExceeded,
				ipv6.ICMPTypeParameterProblem,
				ipv6.ICMPTypeRedirect,
			},
			wantBlock: []ipv6.ICMPType{ipv6.ICMPTypeNeighborSolicitation},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filt := icmpv6FilterFromConfig(tc.cfg)
			for _, typ := range tc.wantAccept {
				if filt.WillBlock(typ) {
					t.Errorf("expected filter to accept %v, but it blocks it", typ)
				}
			}
			for _, typ := range tc.wantBlock {
				if !filt.WillBlock(typ) {
					t.Errorf("expected filter to block %v, but it accepts it", typ)
				}
			}
		})
	}
}

// fakeErrICMPFilterConn implements icmpv6FilterSetter and always fails,
// simulating a platform (or test double) without ICMP6_FILTER support.
type fakeErrICMPFilterConn struct{}

func (f *fakeErrICMPFilterConn) SetICMPFilter(filt *ipv6.ICMPFilter) error {
	return errUnsupportedICMPFilter
}

var errUnsupportedICMPFilter = errors.New("ICMP6_FILTER not supported")

// TestApplyICMPv6Filter_ErrorIsNonFatal proves applyICMPv6Filter swallows a
// SetICMPFilter error instead of panicking or propagating it - matching the
// existing best-effort `_ = conn.SetControlMessage(...)` calls nearby.
func TestApplyICMPv6Filter_ErrorIsNonFatal(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("applyICMPv6Filter panicked on SetICMPFilter error: %v", r)
		}
	}()
	applyICMPv6Filter(&fakeErrICMPFilterConn{})
}

// TestStartIPv6_FilterErrorDoesNotFailStart exercises the real Start() path:
// even though the fake conn returned by ListenPacket cannot actually honor
// ICMP6_FILTER (it isn't a real syscall.Conn), Start() must still succeed.
func TestStartIPv6_FilterErrorDoesNotFailStart(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	p.Source = "2001:db8::1"
	if err := p.Start(10*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("expected Start to succeed despite ICMP6_FILTER being unsupported, got: %v", err)
	}
	defer p.Close()
	if p.connV6 == nil {
		t.Fatal("expected connV6 to be set")
	}
}

// TestOpenHopSocketV6_FilterErrorDoesNotFail exercises OpenHopSocket's v6
// branch end to end via the same non-syscall fake conn used elsewhere in
// this package, proving a SetICMPFilter failure there is non-fatal too.
func TestOpenHopSocketV6_FilterErrorDoesNotFail(t *testing.T) {
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return &net.IPAddr{IP: net.ParseIP("2001:4860:4860::8888")}, nil
		},
		ListenPacket: func(network, address string) (net.PacketConn, error) {
			return &fakeNetPacketConn{}, nil
		},
	})
	sock, err := p.OpenHopSocket("2001:4860:4860::8888")
	if err != nil {
		t.Fatalf("expected OpenHopSocket to succeed despite filter being unsupported, got: %v", err)
	}
	defer sock.Close()
	if sock.sendV6 == nil {
		t.Fatal("expected sendV6 to be set")
	}
}
