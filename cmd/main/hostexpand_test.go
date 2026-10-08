package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestExpandHostPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{name: "plain hostname passes through", pattern: "example.com", want: []string{"example.com"}},
		{name: "hyphenated hostname is not a range", pattern: "core-sw-01.lab", want: []string{"core-sw-01.lab"}},
		{name: "plain IPv4 passes through", pattern: "10.0.0.1", want: []string{"10.0.0.1"}},
		{name: "plain IPv6 passes through", pattern: "2001:db8::1", want: []string{"2001:db8::1"}},
		{name: "last-octet range", pattern: "10.0.0.1-3", want: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}},
		{name: "full address range crosses octet", pattern: "10.0.0.254-10.0.1.1", want: []string{"10.0.0.254", "10.0.0.255", "10.0.1.0", "10.0.1.1"}},
		{name: "single-address range", pattern: "10.0.0.5-5", want: []string{"10.0.0.5"}},
		{name: "IPv4 CIDR excludes network and broadcast", pattern: "192.168.1.0/30", want: []string{"192.168.1.1", "192.168.1.2"}},
		{name: "IPv4 /31 keeps both addresses", pattern: "192.168.1.0/31", want: []string{"192.168.1.0", "192.168.1.1"}},
		{name: "IPv4 /32 is a single host", pattern: "192.168.1.7/32", want: []string{"192.168.1.7"}},
		{name: "CIDR with host bits is masked", pattern: "192.168.1.9/30", want: []string{"192.168.1.9", "192.168.1.10"}},
		{name: "IPv6 CIDR keeps every address", pattern: "2001:db8::/127", want: []string{"2001:db8::", "2001:db8::1"}},
		{name: "brace range", pattern: "sw{1..3}.lab", want: []string{"sw1.lab", "sw2.lab", "sw3.lab"}},
		{name: "brace range keeps zero padding", pattern: "sw{08..10}", want: []string{"sw08", "sw09", "sw10"}},
		{name: "multiple braces form a product", pattern: "r{1..2}-p{1..2}", want: []string{"r1-p1", "r1-p2", "r2-p1", "r2-p2"}},
		{name: "brace inside an IPv4 range", pattern: "10.{1..2}.0.1-2", want: []string{"10.1.0.1", "10.1.0.2", "10.2.0.1", "10.2.0.2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandHostPattern(tt.pattern)
			if err != nil {
				t.Fatalf("expandHostPattern(%q): unexpected error: %v", tt.pattern, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expandHostPattern(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestExpandHostPattern_Errors(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr string
	}{
		{name: "descending range", pattern: "10.0.0.9-3", wantErr: "end must not be below start"},
		{name: "last octet out of range", pattern: "10.0.0.1-300", wantErr: "end must be a last octet (0-255) or an IPv4 address"},
		{name: "signed last octet", pattern: "10.0.0.1-+5", wantErr: "end must be a last octet (0-255) or an IPv4 address"},
		{name: "non-numeric range end", pattern: "10.0.0.1-abc", wantErr: "end must be a last octet (0-255) or an IPv4 address"},
		{name: "mixed families in range", pattern: "10.0.0.1-2001:db8::1", wantErr: "start and end must both be IPv4"},
		{name: "IPv6 range", pattern: "2001:db8::1-5", wantErr: "IPv6 ranges are not supported"},
		{name: "IPv4-mapped IPv6 range", pattern: "::ffff:10.0.0.1-5", wantErr: "IPv6 ranges are not supported"},
		{name: "descending brace", pattern: "sw{5..1}", wantErr: "start must not exceed end"},
		{name: "unsupported brace list", pattern: "{tokyo,osaka}-gw", wantErr: "unsupported brace expression"},
		{name: "malformed brace range", pattern: "sw{1-3}.lab", wantErr: "unsupported brace expression"},
		{name: "unbalanced brace", pattern: "sw{1..3.lab", wantErr: "unsupported brace expression"},
		{name: "CIDR too large", pattern: "10.0.0.0/16", wantErr: "expands to more than 1024 hosts"},
		{name: "IPv6 CIDR too large", pattern: "2001:db8::/64", wantErr: "expands to more than 1024 hosts"},
		{name: "range too large", pattern: "10.0.0.0-10.1.0.0", wantErr: "expands to more than 1024 hosts"},
		{name: "brace product too large", pattern: "h{1..100}-{1..100}", wantErr: "expands to more than 1024 hosts"},
		{name: "brace and CIDR product too large", pattern: "10.0.{0..4}.0/24", wantErr: "expands to more than 1024 hosts"},
		{name: "invalid CIDR", pattern: "10.0.0.0/33", wantErr: "invalid CIDR notation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := expandHostPattern(tt.pattern)
			if err == nil {
				t.Fatalf("expandHostPattern(%q): expected error containing %q, got nil", tt.pattern, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expandHostPattern(%q): error %q does not contain %q", tt.pattern, err, tt.wantErr)
			}
		})
	}
}

func TestExpandHostPattern_ErrorsDoNotEchoInput(t *testing.T) {
	// Include files may be read with elevated privileges (setuid install),
	// so pattern errors must not quote the offending text back.
	secret := "s3cret/line-with-content"
	_, err := expandHostPattern(secret)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error %q echoes its input", err)
	}
}

func TestExpandHostPattern_LimitBoundary(t *testing.T) {
	// /22 has 1024 addresses, 1022 usable — within the limit.
	got, err := expandHostPattern("10.0.0.0/22")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1022 {
		t.Fatalf("len = %d, want 1022", len(got))
	}
	// Exactly maxPatternExpansion via brace range is allowed.
	got, err = expandHostPattern(fmt.Sprintf("h{1..%d}", maxPatternExpansion))
	if err != nil {
		t.Fatalf("unexpected error at limit: %v", err)
	}
	if len(got) != maxPatternExpansion {
		t.Fatalf("len = %d, want %d", len(got), maxPatternExpansion)
	}
	if _, err := expandHostPattern(fmt.Sprintf("h{1..%d}", maxPatternExpansion+1)); err == nil {
		t.Fatal("expected error one past the limit")
	}
}

func TestExpandHostEntries(t *testing.T) {
	entries := []hostEntry{
		{Host: "10.0.0.1-2", DSCP: "EF"},
		{Host: "gw.example.com", Name: "gateway"},
		{Host: "10.0.0.9/32", Name: "single"},
	}
	got, err := expandHostEntries(entries, "hosts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []hostEntry{
		{Host: "10.0.0.1", DSCP: "EF"},
		{Host: "10.0.0.2", DSCP: "EF"},
		{Host: "gw.example.com", Name: "gateway"},
		{Host: "10.0.0.9", Name: "single"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandHostEntries_NameOnMultiHostPatternIsRejected(t *testing.T) {
	_, err := expandHostEntries([]hostEntry{{Host: "10.0.0.1-2", Name: "pair"}}, `groups["Core"]`)
	if err == nil {
		t.Fatal("expected error for name on a multi-host pattern")
	}
	if !strings.Contains(err.Error(), `groups["Core"][0]`) || !strings.Contains(err.Error(), "name cannot be used") {
		t.Fatalf("error %q should identify the entry and mention name", err)
	}
}

func TestExpandHostEntries_ErrorsUseUnexpandedIndex(t *testing.T) {
	tests := []struct {
		name    string
		entries []hostEntry
		wantErr string
	}{
		{
			name:    "pattern error quotes the YAML value",
			entries: []hostEntry{{Host: "a"}, {Host: "10.0.0.0/16"}},
			wantErr: `hosts[1] "10.0.0.0/16": expands to more than`,
		},
		{
			name:    "DSCP is checked before expansion",
			entries: []hostEntry{{Host: "10.0.0.1-50"}, {Host: "10.0.1.1-50", DSCP: "bogus"}},
			wantErr: "hosts[1]: dscp:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := expandHostEntries(tt.entries, "hosts")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
