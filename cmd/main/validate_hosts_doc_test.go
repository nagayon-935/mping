package main

import (
	"strings"
	"testing"
)

// ptrTo returns a pointer to v, for the optional (pointer) fields of
// hostsFileYAML.
func ptrTo[T any](v T) *T { return &v }

func TestValidateHostsDoc(t *testing.T) {
	tests := []struct {
		name string
		doc  hostsFileYAML
		// wantErr is a substring the error must contain.
		wantErr string
		// wantAnyErr marks cases that must fail without a message pin.
		// Cases with neither field set must validate.
		wantAnyErr bool
	}{
		{
			name: "valid document",
			doc: hostsFileYAML{
				Hosts: hostEntries("a.com", "b.com"), IntervalMs: ptrTo(500), TimeoutMs: ptrTo(1000),
				PacketSize: ptrTo(56), Count: ptrTo(0),
			},
		},
		{name: "empty hosts", doc: hostsFileYAML{Hosts: hostEntries()}, wantAnyErr: true},
		{name: "nil hosts", doc: hostsFileYAML{}, wantAnyErr: true},
		{name: "blank host entry", doc: hostsFileYAML{Hosts: hostEntries("a.com", "  ")}, wantAnyErr: true},
		{name: "interval below minimum", doc: hostsFileYAML{Hosts: hostEntries("a.com"), IntervalMs: ptrTo(50)}, wantAnyErr: true},
		{name: "interval above maximum", doc: hostsFileYAML{Hosts: hostEntries("a.com"), IntervalMs: ptrTo(99999)}, wantAnyErr: true},
		{name: "timeout below minimum", doc: hostsFileYAML{Hosts: hostEntries("a.com"), TimeoutMs: ptrTo(5)}, wantAnyErr: true},
		{name: "size below minimum", doc: hostsFileYAML{Hosts: hostEntries("a.com"), PacketSize: ptrTo(0)}, wantAnyErr: true},
		{name: "negative count", doc: hostsFileYAML{Hosts: hostEntries("a.com"), Count: ptrTo(-1)}, wantAnyErr: true},
		{name: "valid duration", doc: hostsFileYAML{Hosts: hostEntries("a.com"), Duration: ptrTo("5m")}},
		{name: "malformed duration", doc: hostsFileYAML{Hosts: hostEntries("a.com"), Duration: ptrTo("not-a-duration")}, wantAnyErr: true},
		{name: "negative duration", doc: hostsFileYAML{Hosts: hostEntries("a.com"), Duration: ptrTo("-5s")}, wantAnyErr: true},
		{name: "ipv4 and ipv6 both true", doc: hostsFileYAML{Hosts: hostEntries("a.com"), Ipv4Only: ptrTo(true), Ipv6Only: ptrTo(true)}, wantAnyErr: true},

		// DSCP.
		{name: "global dscp valid", doc: hostsFileYAML{Hosts: hostEntries("a.com"), DSCP: ptrTo("EF")}},
		{name: "global dscp invalid", doc: hostsFileYAML{Hosts: hostEntries("a.com"), DSCP: ptrTo("NOT-A-CODEPOINT")}, wantAnyErr: true},
		{name: "per-host dscp valid", doc: hostsFileYAML{Hosts: []hostEntry{{Host: "a.com", DSCP: "AF41"}}}},
		{
			name:    "per-host dscp out of range names the entry",
			doc:     hostsFileYAML{Hosts: []hostEntry{{Host: "a.com", DSCP: "9999"}}},
			wantErr: "hosts[0]",
		},
		{
			name:    "group host dscp invalid names the group entry",
			doc:     hostsFileYAML{Groups: []groupYAML{{Name: "DC1", Hosts: []hostEntry{{Host: "a.com", DSCP: "bogus"}}}}},
			wantErr: `groups["DC1"][0]`,
		},

		// Thresholds.
		{
			name: "thresholds with warn >= crit",
			doc: hostsFileYAML{Hosts: hostEntries("a.com"), Thresholds: &thresholdsYAML{
				RTTWarn: ptrTo(200), RTTCrit: ptrTo(100),
			}},
			wantAnyErr: true,
		},
		{
			name: "valid thresholds",
			doc: hostsFileYAML{Hosts: hostEntries("a.com"), Thresholds: &thresholdsYAML{
				RTTWarn: ptrTo(30), RTTCrit: ptrTo(120), LossWarn: ptrTo(10.0), LossCrit: ptrTo(70.0),
			}},
		},

		// Groups.
		{name: "groups only", doc: hostsFileYAML{Groups: []groupYAML{{Name: "DC1", Hosts: hostEntries("192.168.1.1", "192.168.1.2")}}}},
		{
			name: "hosts and groups mixed",
			doc: hostsFileYAML{
				Hosts:  hostEntries("google.com"),
				Groups: []groupYAML{{Name: "DC1", Hosts: hostEntries("192.168.1.1")}},
			},
		},
		{name: "empty group name", doc: hostsFileYAML{Groups: []groupYAML{{Name: "", Hosts: hostEntries("192.168.1.1")}}}, wantAnyErr: true},
		{name: "group without hosts", doc: hostsFileYAML{Groups: []groupYAML{{Name: "DC1", Hosts: hostEntries()}}}, wantAnyErr: true},
		{name: "blank host entry in group", doc: hostsFileYAML{Groups: []groupYAML{{Name: "DC1", Hosts: hostEntries("192.168.1.1", "  ")}}}, wantAnyErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHostsDoc(tt.doc)
			switch {
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateHostsDoc error = %v, want one containing %q", err, tt.wantErr)
				}
			case tt.wantAnyErr:
				if err == nil {
					t.Fatal("validateHostsDoc accepted an invalid document")
				}
			default:
				if err != nil {
					t.Fatalf("validateHostsDoc rejected a valid document: %v", err)
				}
			}
		})
	}
}
