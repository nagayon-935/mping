package pinger

import "testing"

func TestParseDSCPNames(t *testing.T) {
	// Table-driven, one case per dscpNameTable entry: verifies the raw DSCP
	// codepoint AND the << 2 shift into the final TOS/TrafficClass byte.
	tests := []struct {
		name    string
		input   string
		wantTOS int
	}{
		{"DF", "DF", 0 << 2},
		{"EF", "EF", 46 << 2},
		{"VA", "VA", 44 << 2},
		{"CS0", "CS0", 0 << 2},
		{"CS1", "CS1", 8 << 2},
		{"CS2", "CS2", 16 << 2},
		{"CS3", "CS3", 24 << 2},
		{"CS4", "CS4", 32 << 2},
		{"CS5", "CS5", 40 << 2},
		{"CS6", "CS6", 48 << 2},
		{"CS7", "CS7", 56 << 2},
		{"AF11", "AF11", 10 << 2},
		{"AF12", "AF12", 12 << 2},
		{"AF13", "AF13", 14 << 2},
		{"AF21", "AF21", 18 << 2},
		{"AF22", "AF22", 20 << 2},
		{"AF23", "AF23", 22 << 2},
		{"AF31", "AF31", 26 << 2},
		{"AF32", "AF32", 28 << 2},
		{"AF33", "AF33", 30 << 2},
		{"AF41", "AF41", 34 << 2},
		{"AF42", "AF42", 36 << 2},
		{"AF43", "AF43", 38 << 2},
		// Case-insensitivity and surrounding whitespace.
		{"lowercase ef", "ef", 46 << 2},
		{"mixed case af41", "Af41", 34 << 2},
		{"whitespace padded", "  EF  ", 46 << 2},
	}

	if len(dscpNameTable) != 23 {
		t.Fatalf("dscpNameTable has %d entries, want 23 (DF, EF, VA, CS0-7, AF11-13, AF21-23, AF31-33, AF41-43)", len(dscpNameTable))
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDSCP(tt.input)
			if err != nil {
				t.Fatalf("ParseDSCP(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.wantTOS {
				t.Errorf("ParseDSCP(%q) = %d, want %d", tt.input, got, tt.wantTOS)
			}
			if got < 0 || got > 255 {
				t.Errorf("ParseDSCP(%q) = %d, out of byte range", tt.input, got)
			}
		})
	}
}

func TestParseDSCPNumeric(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"zero", "0", 0, false},
		{"typical byte value", "184", 184, false}, // 0xB8, the raw EF TOS byte
		{"max byte", "255", 255, false},
		{"negative rejected", "-1", 0, true},
		{"above range rejected", "256", 0, true},
		{"way above range rejected", "1000", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDSCP(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDSCP(%q) expected error, got nil (value=%d)", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDSCP(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDSCP(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestDSCPName(t *testing.T) {
	tests := []struct {
		name string
		tos  int
		want string
	}{
		{"EF byte", 46 << 2, "EF"},
		{"EF byte with ECN bits set", (46 << 2) | 0x3, "EF"}, // ECN must be ignored
		{"CS0 (unmarked)", 0, "CS0"},
		{"AF41", 34 << 2, "AF41"},
		{"unknown codepoint falls back to number", 63 << 2, "63"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DSCPName(tt.tos); got != tt.want {
				t.Errorf("DSCPName(%d) = %q, want %q", tt.tos, got, tt.want)
			}
		})
	}
}

func TestParseDSCPInvalidNames(t *testing.T) {
	invalid := []string{"", "  ", "NOTAREALCODEPOINT", "AF44", "CS8", "ef1", "0x2E"}
	for _, in := range invalid {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseDSCP(in); err == nil {
				t.Errorf("ParseDSCP(%q) expected error, got nil", in)
			}
		})
	}
}
