package ui

// Golden tests that pin down the exact rendered header/data row text for the
// main table across the layouts most likely to break when the column schema
// changes — DNS/ASN column presence, narrow-terminal hiding, and grouped
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
// The table draws no lines between rows, so the sequence returned is
// header, row1, row2, ...
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
			wantHeader: "║SRC   │DST                                                         │       RCV│      LOST│       LOSS%│         RTT│         AVG│         JIT│RTT 60s           │    SIZE│     MTU│   TTL│LAST LOSS   ║",
			wantData:   "║Auto  │example.com (93.184.216.34)                                 │         0│         0│        0.0%│           -│           -│           -│                  │      56│       -│     -│-           ║",
		},
		{
			// DNS's rendered width comes from the same render func used for
			// width measurement and cell text (col.render), and DNS carries
			// its own grow priority.
			name:       "DNSOnly",
			dns:        true,
			wantHeader: "║SRC   │DST                                                │DNS      │       RCV│      LOST│       LOSS%│         RTT│         AVG│         JIT│RTT 60s           │    SIZE│     MTU│   TTL│LAST LOSS  ║",
			wantData:   "║Auto  │example.com (93.184.216.34)                        │8.8.8.8  │         0│         0│        0.0%│           -│           -│           -│                  │      56│       -│     -│-          ║",
		},
		{
			// ASN's width is measured from the same render func used for the
			// cell text (number + operator name, no country code), so the
			// column is sized to its full content instead of truncating to
			// "AS1513...".
			name:       "ASNOnly",
			asn:        true,
			wantHeader: "║SRC   │DST                                       │               ASN│       RCV│      LOST│       LOSS%│         RTT│         AVG│         JIT│RTT 60s           │    SIZE│     MTU│   TTL│LAST LOSS  ║",
			wantData:   "║Auto  │example.com (93.184.216.34)               │  AS15133 EdgeCast│         0│         0│        0.0%│           -│           -│           -│                  │      56│       -│     -│-          ║",
		},
		{
			name:       "DNSAndASN",
			dns:        true,
			asn:        true,
			wantHeader: "║SRC   │DST                             │DNS      │               ASN│       RCV│      LOST│       LOSS%│         RTT│         AVG│         JIT│RTT 60s           │    SIZE│     MTU│   TTL│LAST LOSS  ║",
			wantData:   "║Auto  │example.com (93.184.216.34)     │8.8.8.8  │  AS15133 EdgeCast│         0│         0│        0.0%│           -│           -│           -│                  │      56│       -│     -│-          ║",
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
			// extraRows=1: header and first data row.
			rows := captureTableRows(t, opts, 200, 50, "DST", 1)
			if rows[0] != sc.wantHeader {
				t.Errorf("header mismatch:\n got  %q\n want %q", rows[0], sc.wantHeader)
			}
			if rows[1] != sc.wantData {
				t.Errorf("data row mismatch:\n got  %q\n want %q", rows[1], sc.wantData)
			}
		})
	}
}

// TestColumnGolden_NarrowLayout pins a narrow terminal: supplementary
// columns are hidden (and named in the pane title) instead of cutting cells.
func TestColumnGolden_NarrowLayout(t *testing.T) {
	const (
		wantHeader = "║DST                        │RCV│LOST│LOSS%│RTT│AVG│JIT│TTL│LAST LOSS║"
		wantData   = "║example.com (93.184.216.34)│  0│   0│ 0.0%│  -│  -│  -│  -│-        ║"
	)
	target := stats.NewTargetStats("example.com")
	target.SetIP("93.184.216.34")
	opts := RunOptions{
		Targets:    []*stats.TargetStats{target},
		Interval:   50 * time.Millisecond,
		Timeout:    50 * time.Millisecond,
		PacketSize: 56,
	}
	rows := captureTableRows(t, opts, 70, 30, "DST", 1)
	if rows[0] != wantHeader {
		t.Errorf("header mismatch:\n got  %q\n want %q", rows[0], wantHeader)
	}
	if rows[1] != wantData {
		t.Errorf("data row mismatch:\n got  %q\n want %q", rows[1], wantData)
	}
}

func TestColumnGolden_Groups(t *testing.T) {
	const (
		wantHeader        = "║SRC   │DST                                                         │       RCV│      LOST│       LOSS%│         RTT│         AVG│         JIT│RTT 60s           │    SIZE│     MTU│   TTL│LAST LOSS   ║"
		wantUngrouped     = "║Auto  │standalone.example.com (10.0.0.1)                           │         0│         0│        0.0%│           -│           -│           -│                  │      56│       -│     -│-           ║"
		wantSpacer        = "║      │                                                            │          │          │            │            │            │            │                  │        │        │      │            ║"
		wantGroupHeader   = "║      │ ▸ core  (1 hosts)                                          │          │          │            │            │            │            │                  │        │        │      │            ║"
		wantSubHeader     = wantHeader
		wantGroupedTarget = "║Auto  │grouped.example.com (10.0.0.2)                              │         0│         0│        0.0%│           -│           -│           -│                  │      56│       -│     -│-           ║"
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
	// Row order: header, ungrouped-target, spacer, group-header (banner),
	// subheader (column header repeated), grouped-target.
	rows := captureTableRows(t, opts, 200, 50, "DST", 5)
	if rows[0] != wantHeader {
		t.Errorf("header mismatch:\n got  %q\n want %q", rows[0], wantHeader)
	}
	if rows[1] != wantUngrouped {
		t.Errorf("ungrouped row mismatch:\n got  %q\n want %q", rows[1], wantUngrouped)
	}
	if rows[2] != wantSpacer {
		t.Errorf("spacer row mismatch:\n got  %q\n want %q", rows[2], wantSpacer)
	}
	if rows[3] != wantGroupHeader {
		t.Errorf("group header row mismatch:\n got  %q\n want %q", rows[3], wantGroupHeader)
	}
	if rows[4] != wantSubHeader {
		t.Errorf("subheader row mismatch:\n got  %q\n want %q", rows[4], wantSubHeader)
	}
	if rows[5] != wantGroupedTarget {
		t.Errorf("grouped target row mismatch:\n got  %q\n want %q", rows[5], wantGroupedTarget)
	}
}
