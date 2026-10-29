package ui

// Golden tests that pin down the exact rendered header/data row text for the
// main table across the layouts most likely to break when the column schema
// changes — DNS/ASN column presence, compact fallback, and grouped
// rendering.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

// captureTableRows runs Run() against a fixed-size SimulationScreen, waits
// for the first frame that shows headerMarker, quits, and returns that
// frame's headerMarker row plus extraRows rows below it, each right-trimmed
// of padding. The header row is located by content rather than a hardcoded
// y-offset so the helper survives unrelated layout changes above the table
// pane.
//
// SetBorders(true) draws a border line between every table row, so the
// sequence returned is header, border, row1, border, row2, ... — callers
// pick out actual content at even offsets (rows[0], rows[2], rows[4], ...).
func captureTableRows(t *testing.T, opts RunOptions, width, height int, headerMarker string, extraRows int) []string {
	t.Helper()
	h := startTUI(t, opts, width, height)
	var rows []string
	h.waitFrame(fmt.Sprintf("a header row containing %q", headerMarker), func(frame string) bool {
		lines := strings.Split(frame, "\n")
		for y := 0; y+extraRows < height; y++ {
			if !strings.Contains(lines[y], headerMarker) {
				continue
			}
			rows = make([]string, extraRows+1)
			for i := range rows {
				rows[i] = strings.TrimRight(lines[y+i], " ")
			}
			return true
		}
		return false
	})
	h.quit()
	return rows
}

func TestColumnGolden_FullLayout(t *testing.T) {
	scenarios := []struct {
		name       string
		dns        bool
		asn        bool
		wantHeader string
		wantData   string
	}{
		{
			name:       "NoDNSNoASN",
			wantHeader: "║│Src IP  │Dst IP                       │   Success│     Loss│  Loss Ratio│         RTT│         Avg│      Jitter│    Size│     MTU│   TTL│Error                                    │Last Loss        │║",
			wantData:   "║│Auto    │example.com (93.184.216.34)  │         0│        0│        0.0%│           -│           -│           -│      56│       -│     -│                                         │-                │║",
		},
		{
			// DNS's rendered width comes from the same render func used for
			// width measurement and cell text (col.render), and DNS carries
			// its own grow priority.
			name:       "DNSOnly",
			dns:        true,
			wantHeader: "║│Src IP  │Dst IP                       │DNS     │  Success│    Loss│ Loss Ratio│        RTT│        Avg│     Jitter│   Size│    MTU│   TTL│Error                                    │Last Loss       │║",
			wantData:   "║│Auto    │example.com (93.184.216.34)  │8.8.8.8 │        0│       0│       0.0%│          -│          -│          -│     56│      -│     -│                                         │-               │║",
		},
		{
			// ASN's width is measured from the same render func used for the
			// cell text (number + operator name, no country code), so the
			// column is sized to its full content instead of truncating to
			// "AS1513...".
			name:       "ASNOnly",
			asn:        true,
			wantHeader: "║│Src IP │Dst IP                      │             ASN│  Success│   Loss│Loss Ratio│        RTT│        Avg│    Jitter│  Size│   MTU│  TTL│Error                                    │Last Loss       │║",
			wantData:   "║│Auto   │example.com (93.184.216.34) │AS15133 EdgeCast│        0│      0│      0.0%│          -│          -│         -│    56│     -│    -│                                         │-               │║",
		},
		{
			name:       "DNSAndASN",
			dns:        true,
			asn:        true,
			wantHeader: "║│Src IP│Dst IP                    │DNS    │             ASN│ Success│   Loss│Loss Ratio│       RTT│       Avg│    Jitter│  Size│   MTU│  TTL│Error                                   │Last Loss      │║",
			wantData:   "║│Auto  │example.com (93.184.216...│8.8.8.8│AS15133 EdgeCast│       0│      0│      0.0%│         -│         -│         -│    56│     -│    -│                                        │-              │║",
		},
	}
	for i := range scenarios {
		sc := &scenarios[i]
		t.Run(sc.name, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			target.SetIP("93.184.216.34")
			if sc.dns {
				target.SetDNSServer("8.8.8.8")
			}
			if sc.asn {
				target.SetASNInfo("AS15133", "US", "EdgeCast")
			}
			opts := RunOptions{
				Targets:    []*stats.TargetStats{target},
				Interval:   50 * time.Millisecond,
				Timeout:    50 * time.Millisecond,
				PacketSize: 56,
				ASNEnabled: sc.asn,
			}
			// extraRows=2: header, border, first data row. Marker is "Dst IP"
			// rather than "Src IP" since Src IP can shrink to "Sr..." under
			// heavy column pressure (DNS+ASN enabled).
			rows := captureTableRows(t, opts, 200, 50, "Dst IP", 2)
			if rows[0] != sc.wantHeader {
				t.Errorf("header mismatch:\n got  %q\n want %q", rows[0], sc.wantHeader)
			}
			if rows[2] != sc.wantData {
				t.Errorf("data row mismatch:\n got  %q\n want %q", rows[2], sc.wantData)
			}
		})
	}
}

func TestColumnGolden_CompactLayout(t *testing.T) {
	const (
		wantHeader = "║│Host    │Path            │Stats                          │Error   │║"
		wantData   = "║│examp...│Auto -> 93.18...│S:0 L:0 Loss:0.0% RTT:-        │        │║"
	)
	target := stats.NewTargetStats("example.com")
	target.SetIP("93.184.216.34")
	opts := RunOptions{
		Targets:    []*stats.TargetStats{target},
		Interval:   50 * time.Millisecond,
		Timeout:    50 * time.Millisecond,
		PacketSize: 56,
	}
	rows := captureTableRows(t, opts, 70, 30, "Host", 2)
	if rows[0] != wantHeader {
		t.Errorf("header mismatch:\n got  %q\n want %q", rows[0], wantHeader)
	}
	if rows[2] != wantData {
		t.Errorf("data row mismatch:\n got  %q\n want %q", rows[2], wantData)
	}
}

func TestColumnGolden_Groups(t *testing.T) {
	const (
		wantHeader        = "║│Src IP            │Dst IP                             │   Success│    Loss│ Loss Ratio│         RTT│        Avg│     Jitter│   Size│    MTU│   TTL│Error                                    │Last L…│║"
		wantUngrouped     = "║│Auto              │standalone.example.com (10.0.0.1)  │         0│       0│       0.0%│           -│          -│          -│     56│      -│     -│                                         │-     …│║"
		wantSpacer        = "║│                  │                                   │          │        │           │            │           │           │       │       │      │                                         │       │║"
		wantGroupHeader   = "║│ ▸ core  (1 hosts)│                                   │          │        │           │            │           │           │       │       │      │                                         │       │║"
		wantSubHeader     = wantHeader
		wantGroupedTarget = "║│Auto              │grouped.example.com (10.0.0.2)     │         0│       0│       0.0%│           -│          -│          -│     56│      -│     -│                                         │-     …│║"
	)
	ungrouped := stats.NewTargetStats("standalone.example.com")
	ungrouped.SetIP("10.0.0.1")
	grouped := stats.NewTargetStats("grouped.example.com")
	grouped.SetIP("10.0.0.2")
	opts := RunOptions{
		Targets:    []*stats.TargetStats{ungrouped, grouped},
		Interval:   50 * time.Millisecond,
		Timeout:    50 * time.Millisecond,
		PacketSize: 56,
		Groups:     []TargetGroup{{Name: "core", Indices: []int{1}}},
	}
	// Row order (each logical row followed by a border line):
	// header, border, ungrouped-target, border, spacer, border,
	// group-header (banner), border, subheader (column header repeated),
	// border, grouped-target.
	rows := captureTableRows(t, opts, 200, 50, "Src IP", 10)
	if rows[0] != wantHeader {
		t.Errorf("header mismatch:\n got  %q\n want %q", rows[0], wantHeader)
	}
	if rows[2] != wantUngrouped {
		t.Errorf("ungrouped row mismatch:\n got  %q\n want %q", rows[2], wantUngrouped)
	}
	if rows[4] != wantSpacer {
		t.Errorf("spacer row mismatch:\n got  %q\n want %q", rows[4], wantSpacer)
	}
	if rows[6] != wantGroupHeader {
		t.Errorf("group header row mismatch:\n got  %q\n want %q", rows[6], wantGroupHeader)
	}
	if rows[8] != wantSubHeader {
		t.Errorf("subheader row mismatch:\n got  %q\n want %q", rows[8], wantSubHeader)
	}
	if rows[10] != wantGroupedTarget {
		t.Errorf("grouped target row mismatch:\n got  %q\n want %q", rows[10], wantGroupedTarget)
	}
}
