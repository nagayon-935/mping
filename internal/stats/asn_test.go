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
