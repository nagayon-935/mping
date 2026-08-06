package main

import "testing"

// ---- buildPingerOptions: DSCP / TargetDSCP construction ----

func TestBuildPingerOptions_NoDSCPConfigured(t *testing.T) {
	cfg := config{}
	specs := []targetSpec{{Host: "example.com"}}
	opts := buildPingerOptions(cfg, "ip", nil, specs)
	if opts.DSCP != nil {
		t.Fatalf("opts.DSCP = %v, want nil when cfg.dscp is unset", opts.DSCP)
	}
	if opts.TargetDSCP != nil {
		t.Fatalf("opts.TargetDSCP = %v, want nil when no spec has a DSCP override", opts.TargetDSCP)
	}
}

func TestBuildPingerOptions_GlobalDSCP(t *testing.T) {
	cfg := config{dscp: "EF"}
	specs := []targetSpec{{Host: "example.com"}}
	opts := buildPingerOptions(cfg, "ip", nil, specs)
	if opts.DSCP == nil || *opts.DSCP != 46<<2 {
		t.Fatalf("opts.DSCP = %v, want %d", opts.DSCP, 46<<2)
	}
}

func TestBuildPingerOptions_GlobalDSCPExplicitCS0(t *testing.T) {
	// CS0 parses to 0 — must still produce a non-nil *int (distinguishing
	// "explicitly configured to 0" from "not configured"), matching
	// pinger.Options.DSCP's pointer contract.
	cfg := config{dscp: "CS0"}
	specs := []targetSpec{{Host: "example.com"}}
	opts := buildPingerOptions(cfg, "ip", nil, specs)
	if opts.DSCP == nil {
		t.Fatal("opts.DSCP = nil, want non-nil pointer to 0 for explicit CS0")
	}
	if *opts.DSCP != 0 {
		t.Fatalf("*opts.DSCP = %d, want 0", *opts.DSCP)
	}
}

func TestBuildPingerOptions_PerTargetDSCP(t *testing.T) {
	cfg := config{dscp: "CS0"}
	specs := []targetSpec{
		{Host: "2001:db8::1", DSCP: "EF"},
		{Host: "2001:db8::1", PinnedIP: "2001:db8::2", DSCP: "AF41"},
		{Host: "plain.example.com"},
	}
	opts := buildPingerOptions(cfg, "ip", nil, specs)
	if opts.DSCP == nil || *opts.DSCP != 0 {
		t.Fatalf("opts.DSCP = %v, want pointer to 0 (CS0)", opts.DSCP)
	}
	if got := opts.TargetDSCP["2001:db8::1"]; got != 46<<2 {
		t.Fatalf("TargetDSCP[2001:db8::1] = %d, want %d", got, 46<<2)
	}
	// specs[1].display() is "2001:db8::1 (2001:db8::2)" (Host != PinnedIP),
	// matching the same key convention buildPingerOptions's pre-existing
	// `pinned` map already uses.
	wantKey := specs[1].display()
	if got := opts.TargetDSCP[wantKey]; got != 34<<2 {
		t.Fatalf("TargetDSCP[%q] = %d, want %d", wantKey, got, 34<<2)
	}
	if _, ok := opts.TargetDSCP["plain.example.com"]; ok {
		t.Fatal("TargetDSCP should have no entry for a target with no override")
	}
}

func TestBuildPingerOptions_InvalidPerTargetDSCPIgnored(t *testing.T) {
	// buildPingerOptions's doc explains this is unreachable in practice
	// (parseArgs/validateHostsDoc already reject a bad spec) — this test
	// locks in the documented fallback behavior rather than a panic/error.
	cfg := config{}
	specs := []targetSpec{{Host: "example.com", DSCP: "not-a-codepoint"}}
	opts := buildPingerOptions(cfg, "ip", nil, specs)
	if opts.TargetDSCP != nil {
		t.Fatalf("TargetDSCP = %v, want nil (invalid override silently skipped)", opts.TargetDSCP)
	}
}

// ---- parseArgs: --dscp flag ----

func TestParseArgs_DSCPValid(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"--dscp", "EF", "example.com"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.dscp != "EF" {
		t.Fatalf("cfg.dscp = %q, want %q", cfg.dscp, "EF")
	}
}

func TestParseArgs_DSCPInvalidRejected(t *testing.T) {
	_, _, _, _, err := parseArgs([]string{"--dscp", "NOT-A-CODEPOINT", "example.com"})
	if err == nil {
		t.Fatal("expected error for invalid --dscp value")
	}
}

func TestParseArgs_DSCPNumeric(t *testing.T) {
	cfg, _, _, _, err := parseArgs([]string{"--dscp", "184", "example.com"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.dscp != "184" {
		t.Fatalf("cfg.dscp = %q, want %q", cfg.dscp, "184")
	}
}

func TestParseArgs_DSCPOutOfRangeRejected(t *testing.T) {
	_, _, _, _, err := parseArgs([]string{"--dscp", "300", "example.com"})
	if err == nil {
		t.Fatal("expected error for out-of-range --dscp value")
	}
}

// ---- dscpColumnEnabled ----

func TestDSCPColumnEnabled(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config
		hosts []targetSpec
		want  bool
	}{
		{"nothing configured", config{}, []targetSpec{{Host: "a.com"}}, false},
		{"global dscp set", config{dscp: "EF"}, []targetSpec{{Host: "a.com"}}, true},
		{"per-target override only", config{}, []targetSpec{{Host: "a.com", DSCP: "EF"}}, true},
		{"mixed hosts, one overridden", config{}, []targetSpec{{Host: "a.com"}, {Host: "b.com", DSCP: "CS0"}}, true},
		{"no hosts", config{}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dscpColumnEnabled(tt.cfg, tt.hosts); got != tt.want {
				t.Errorf("dscpColumnEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
