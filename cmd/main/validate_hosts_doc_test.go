package main

import (
	"strings"
	"testing"

	"github.com/nagayon-935/mping/internal/hostsfile"
)

// ptrTo returns a pointer to v, for the optional (pointer) fields of
// hostsfile.Document.
func ptrTo[T any](v T) *T { return &v }

func TestValidateHostsDoc(t *testing.T) {
	tests := []struct {
		name string
		doc  hostsfile.Document
		// wantErr is a substring the error must contain.
		wantErr string
		// wantAnyErr marks cases that must fail without a message pin.
		// Cases with neither field set must validate.
		wantAnyErr bool
	}{
		{
			name: "valid document",
			doc: hostsfile.Document{
				Hosts: hostEntries("a.com", "b.com"), IntervalMs: ptrTo(500), TimeoutMs: ptrTo(1000),
				PacketSize: ptrTo(56), Count: ptrTo(0),
			},
		},
		{name: "empty hosts", doc: hostsfile.Document{Hosts: hostEntries()}, wantAnyErr: true},
		{name: "nil hosts", doc: hostsfile.Document{}, wantAnyErr: true},
		{name: "blank host entry", doc: hostsfile.Document{Hosts: hostEntries("a.com", "  ")}, wantAnyErr: true},
		{name: "interval below minimum", doc: hostsfile.Document{Hosts: hostEntries("a.com"), IntervalMs: ptrTo(50)}, wantAnyErr: true},
		{name: "interval above maximum", doc: hostsfile.Document{Hosts: hostEntries("a.com"), IntervalMs: ptrTo(99999)}, wantAnyErr: true},
		{name: "timeout below minimum", doc: hostsfile.Document{Hosts: hostEntries("a.com"), TimeoutMs: ptrTo(5)}, wantAnyErr: true},
		{name: "size below minimum", doc: hostsfile.Document{Hosts: hostEntries("a.com"), PacketSize: ptrTo(0)}, wantAnyErr: true},
		{name: "negative count", doc: hostsfile.Document{Hosts: hostEntries("a.com"), Count: ptrTo(-1)}, wantAnyErr: true},
		{name: "valid duration", doc: hostsfile.Document{Hosts: hostEntries("a.com"), Duration: ptrTo("5m")}},
		{name: "malformed duration", doc: hostsfile.Document{Hosts: hostEntries("a.com"), Duration: ptrTo("not-a-duration")}, wantAnyErr: true},
		{name: "negative duration", doc: hostsfile.Document{Hosts: hostEntries("a.com"), Duration: ptrTo("-5s")}, wantAnyErr: true},
		{name: "ipv4 and ipv6 both true", doc: hostsfile.Document{Hosts: hostEntries("a.com"), Ipv4Only: ptrTo(true), Ipv6Only: ptrTo(true)}, wantAnyErr: true},

		// DSCP.
		{name: "global dscp valid", doc: hostsfile.Document{Hosts: hostEntries("a.com"), DSCP: ptrTo("EF")}},
		{name: "global dscp invalid", doc: hostsfile.Document{Hosts: hostEntries("a.com"), DSCP: ptrTo("NOT-A-CODEPOINT")}, wantAnyErr: true},
		{name: "per-host dscp valid", doc: hostsfile.Document{Hosts: []hostsfile.Entry{{Host: "a.com", DSCP: "AF41"}}}},
		{
			name:    "per-host dscp out of range names the entry",
			doc:     hostsfile.Document{Hosts: []hostsfile.Entry{{Host: "a.com", DSCP: "9999"}}},
			wantErr: "hosts[0]",
		},
		{
			name:    "group host dscp invalid names the group entry",
			doc:     hostsfile.Document{Groups: []hostsfile.Group{{Name: "DC1", Hosts: []hostsfile.Entry{{Host: "a.com", DSCP: "bogus"}}}}},
			wantErr: `groups["DC1"][0]`,
		},

		// Thresholds.
		{
			name: "thresholds with warn >= crit",
			doc: hostsfile.Document{Hosts: hostEntries("a.com"), Thresholds: &hostsfile.Thresholds{
				RTTWarn: ptrTo(200), RTTCrit: ptrTo(100),
			}},
			wantAnyErr: true,
		},
		{
			name: "valid thresholds",
			doc: hostsfile.Document{Hosts: hostEntries("a.com"), Thresholds: &hostsfile.Thresholds{
				RTTWarn: ptrTo(30), RTTCrit: ptrTo(120), LossWarn: ptrTo(10.0), LossCrit: ptrTo(70.0),
			}},
		},

		// Groups.
		{name: "groups only", doc: hostsfile.Document{Groups: []hostsfile.Group{{Name: "DC1", Hosts: hostEntries("192.168.1.1", "192.168.1.2")}}}},
		{
			name: "hosts and groups mixed",
			doc: hostsfile.Document{
				Hosts:  hostEntries("google.com"),
				Groups: []hostsfile.Group{{Name: "DC1", Hosts: hostEntries("192.168.1.1")}},
			},
		},
		{name: "empty group name", doc: hostsfile.Document{Groups: []hostsfile.Group{{Name: "", Hosts: hostEntries("192.168.1.1")}}}, wantAnyErr: true},
		{name: "group without hosts", doc: hostsfile.Document{Groups: []hostsfile.Group{{Name: "DC1", Hosts: hostEntries()}}}, wantAnyErr: true},
		{name: "blank host entry in group", doc: hostsfile.Document{Groups: []hostsfile.Group{{Name: "DC1", Hosts: hostEntries("192.168.1.1", "  ")}}}, wantAnyErr: true},
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
