package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/rivo/tview"
)

func TestInferInitialTTL(t *testing.T) {
	tests := []struct {
		name    string
		lastTTL int
		want    string
	}{
		{"zero", 0, "-"},
		{"negative", -1, "-"},
		{"linux one hop away", 63, "64"},
		{"linux at boundary", 64, "64"},
		{"windows one hop above linux boundary", 65, "128"},
		{"windows one hop away", 127, "128"},
		{"windows at boundary", 128, "128"},
		{"network device one hop above windows boundary", 129, "255"},
		{"network device at max", 255, "255"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferInitialTTL(tt.lastTTL); got != tt.want {
				t.Fatalf("inferInitialTTL(%d) = %q, want %q", tt.lastTTL, got, tt.want)
			}
		})
	}
}

func TestHopCountString(t *testing.T) {
	tests := []struct {
		name string
		hops []string
		want string
	}{
		{"nil", nil, "-"},
		{"empty", []string{}, "-"},
		{"one hop", []string{"1.1.1.1"}, "1"},
		{"three hops", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}, "3"},
		{"with unreachable", []string{"1.1.1.1", "*", "8.8.8.8"}, "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hopCountString(tt.hops); got != tt.want {
				t.Fatalf("hopCountString(%v) = %q, want %q", tt.hops, got, tt.want)
			}
		})
	}
}

func TestWrapHops(t *testing.T) {
	tests := []struct {
		name     string
		hops     []string
		maxWidth int
		want     []string
	}{
		{"nil hops", nil, 80, nil},
		{"empty hops", []string{}, 80, nil},
		{"zero maxWidth", []string{"1.1.1.1"}, 0, nil},
		{
			"all fit on one line",
			[]string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			80,
			[]string{"1.1.1.1 -> 2.2.2.2 -> 3.3.3.3"},
		},
		{
			// "1.1.1.1 -> 2.2.2.2" = 18 chars fits, adding " -> 3.3.3.3" = 29 > 20
			"wraps at hop boundary",
			[]string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			20,
			[]string{"1.1.1.1 -> 2.2.2.2", "3.3.3.3"},
		},
		{
			// maxWidth too narrow for even two hops together: each hop on own line
			"each hop on its own line",
			[]string{"1.1.1.1", "2.2.2.2"},
			7,
			[]string{"1.1.1.1", "2.2.2.2"},
		},
		{
			// single hop wider than maxWidth is still returned as-is
			"single hop wider than maxWidth",
			[]string{"192.168.100.200"},
			5,
			[]string{"192.168.100.200"},
		},
		{
			// first hop wider than maxWidth, second hop fits its own line
			"long first hop forces each on own line",
			[]string{"192.168.100.200", "10.0.0.1"},
			5,
			[]string{"192.168.100.200", "10.0.0.1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapHops(tt.hops, tt.maxWidth)
			if len(got) != len(tt.want) {
				t.Fatalf("wrapHops(%v, %d) = %v, want %v", tt.hops, tt.maxWidth, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("wrapHops(%v, %d)[%d] = %q, want %q", tt.hops, tt.maxWidth, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDisplaySourceIPForDst(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		src4     string
		src6     string
		expected string
	}{
		{
			name:     "ipv4 destination uses ipv4 source",
			dst:      "8.8.8.8",
			src4:     "10.0.0.2",
			src6:     "2001:db8::2",
			expected: "10.0.0.2",
		},
		{
			name:     "ipv6 destination with zone uses ipv6 source",
			dst:      "fe80::1%en0",
			src4:     "10.0.0.2",
			src6:     "fe80::2%en0",
			expected: "fe80::2%en0",
		},
		{
			name:     "ipv6 destination falls back to auto when no ipv6 source",
			dst:      "2001:4860:4860::8888",
			src4:     "10.0.0.2",
			src6:     "",
			expected: "Auto",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displaySourceIPForDst(tt.dst, tt.src4, tt.src6)
			if got != tt.expected {
				t.Fatalf("displaySourceIPForDst(%q): got %q, want %q", tt.dst, got, tt.expected)
			}
		})
	}
}

func TestAppendErrorLog(t *testing.T) {
	view := tview.NewTextView()
	logs := []string{}
	appendErrorLog(&logs, view, "one")
	appendErrorLog(&logs, view, "two")
	if len(logs) != 2 {
		t.Fatalf("logs len: got %d", len(logs))
	}
	if !strings.Contains(view.GetText(false), "two") {
		t.Fatalf("expected latest log in text")
	}
}

func TestNormalizeWriteIP(t *testing.T) {
	msg := normalizeWriteIP("write ip 0.0.0.0->1.1.1.1: x", "10.0.0.2")
	if !strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("expected replaced source ip, got %q", msg)
	}
	msg = normalizeWriteIP("write ip 0.0.0.0->1.1.1.1: x", "Auto")
	if strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("unexpected replacement: %q", msg)
	}
	msg = normalizeWriteIP("other error", "10.0.0.2")
	if msg != "other error" {
		t.Fatalf("unexpected change: %q", msg)
	}
}

func TestBuildErrorLogMessage(t *testing.T) {
	view := stats.TargetView{Host: "example.com"}
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msg := buildErrorLogMessage(view, "10.0.0.2", "write ip 0.0.0.0->1.1.1.1: x", ts)
	if !strings.Contains(msg, "example.com") || !strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("unexpected msg: %q", msg)
	}
}

func TestUpdateAlertState(t *testing.T) {
	view := stats.TargetView{
		Host:    "example.com",
		LastRTT: 300 * time.Millisecond,
		Jitter:  60 * time.Millisecond,
	}
	state, msgs := updateAlertState(view, "10.0.0.2", 90.0, time.Now(), alertFlags{})
	if !state.lossRed || !state.rttRed || !state.jitterRed {
		t.Fatalf("expected alert flags set: %+v", state)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	clearView := stats.TargetView{Host: "example.com"}
	state2, msgs2 := updateAlertState(clearView, "10.0.0.2", 0.0, time.Now(), state)
	if state2.lossRed || state2.rttRed || state2.jitterRed {
		t.Fatalf("expected flags cleared: %+v", state2)
	}
	if len(msgs2) != 0 {
		t.Fatalf("expected no messages, got %d", len(msgs2))
	}
}

// ---- appendErrorLog truncation ----

func TestAppendErrorLog_Truncation(t *testing.T) {
	view := tview.NewTextView()
	logs := make([]string, 0)
	// Fill to exactly 1000 entries
	for i := 0; i < 1000; i++ {
		logs = append(logs, "entry")
	}
	// Adding one more should trigger truncation (remove oldest entry)
	appendErrorLog(&logs, view, "new-entry")
	if len(logs) != 1000 {
		t.Errorf("expected 1000 after truncation, got %d", len(logs))
	}
	if logs[len(logs)-1] != "new-entry" {
		t.Errorf("last entry should be new-entry, got %q", logs[len(logs)-1])
	}
}

// ---- displaySourceIPForDst: IPv6-like string without sourceIPv6 ----

func TestDisplaySourceIPForDst_ColonNoSrc6(t *testing.T) {
	// String contains ":" but is not parseable as IP → falls through to colon check
	got := displaySourceIPForDst("not::valid::ip", "", "")
	if got != "Auto" {
		t.Errorf("expected Auto, got %q", got)
	}
	got = displaySourceIPForDst("not::valid::ip", "10.0.0.1", "")
	if got != "Auto" {
		t.Errorf("expected Auto for IPv6-like without src6, got %q", got)
	}
}

// ---- displaySourceIPForDst: IPv4 auto (no sourceIPv4) ----

func TestDisplaySourceIPForDst_IPv4Auto(t *testing.T) {
	// IPv4 destination but no sourceIPv4 → "Auto"
	got := displaySourceIPForDst("8.8.8.8", "", "2001:db8::1")
	if got != "Auto" {
		t.Errorf("expected Auto for IPv4 dst without src4, got %q", got)
	}
}

// ---- displaySourceIPForDst: IPv6 dst with sourceIPv6 set ----

func TestDisplaySourceIPForDst_IPv6WithSrc6(t *testing.T) {
	got := displaySourceIPForDst("2001:db8::1", "", "2001:db8::cafe")
	if got != "2001:db8::cafe" {
		t.Errorf("IPv6 dst with src6: got %q, want %q", got, "2001:db8::cafe")
	}
}

// ---- displaySourceIPForDst: covers % zone ID path and remaining branches ----

func TestDisplaySourceIPForDst_ZoneID(t *testing.T) {
	// IPv6 with zone ID (e.g. "fe80::1%eth0") → strip % and treat as IPv6
	got := displaySourceIPForDst("fe80::1%eth0", "", "2001::cafe")
	if got != "2001::cafe" {
		t.Errorf("zone ID: got %q, want %q", got, "2001::cafe")
	}
}

func TestDisplaySourceIPForDst_HostnameWithSrc4(t *testing.T) {
	// Non-IP hostname with no colon → falls to final IPv4 check
	got := displaySourceIPForDst("hostname.local", "10.0.0.1", "")
	if got != "10.0.0.1" {
		t.Errorf("hostname with src4: got %q, want %q", got, "10.0.0.1")
	}
}

func TestDisplaySourceIPForDst_ColonWithSrc6(t *testing.T) {
	// Non-IP with colon and sourceIPv6 set → return sourceIPv6
	got := displaySourceIPForDst("not::valid::ip", "10.0.0.1", "2001::cafe")
	if got != "2001::cafe" {
		t.Errorf("colon with src6: got %q, want %q", got, "2001::cafe")
	}
}
