package stats

import "testing"

func TestFormatASN(t *testing.T) {
	tests := []struct {
		name   string
		number string
		org    string
		want   string
	}{
		{"number and org", "AS15169", "Google LLC", "AS15169 Google LLC"},
		{"number only, org unknown", "AS15169", "", "AS15169"},
		{"empty number", "", "Google LLC", ""},
		{"both empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatASN(tt.number, tt.org); got != tt.want {
				t.Errorf("FormatASN(%q, %q) = %q, want %q", tt.number, tt.org, got, tt.want)
			}
		})
	}
}

func TestFormatHop(t *testing.T) {
	tests := []struct {
		name, ip, asn, org, ptr, want string
	}{
		{"no responder", "", "AS1", "Org", "name", "*"},
		{"address only", "10.0.0.1", "", "", "", "10.0.0.1"},
		{"with AS", "8.8.8.8", "AS15169", "Google LLC", "", "8.8.8.8(AS15169 Google LLC)"},
		{"with PTR", "8.8.8.8", "", "", "dns.google", "8.8.8.8 dns.google"},
		{"with both", "8.8.8.8", "AS15169", "", "dns.google", "8.8.8.8(AS15169) dns.google"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatHop(tt.ip, tt.asn, tt.org, tt.ptr); got != tt.want {
				t.Errorf("FormatHop = %q, want %q", got, tt.want)
			}
		})
	}
}
