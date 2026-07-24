package ui

// TD-21 stage①: golden tests that pin down the exact rendered header/data
// row text for the main table across the layouts most likely to break
// during the column-model refactor (TD-21②③) — DNS/ASN column presence,
// compact fallback, and grouped rendering. These must pass unchanged before
// and after introducing the column struct.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// captureTableRows runs Run() against a fixed-size SimulationScreen, waits
// for at least one render tick, quits, and returns headerMarker's row plus
// extraRows rows below it, each right-trimmed of padding. The header row is
// located by content rather than a hardcoded y-offset so the helper survives
// unrelated layout changes above the table pane.
//
// SetBorders(true) draws a border line between every table row, so the
// sequence returned is header, border, row1, border, row2, ... — callers
// pick out actual content at even offsets (rows[0], rows[2], rows[4], ...).
func captureTableRows(t *testing.T, opts RunOptions, width, height int, headerMarker string, extraRows int) []string {
	t.Helper()
	orig := newApplication
	t.Cleanup(func() { newApplication = orig })

	screenCh := make(chan tcell.SimulationScreen, 1)
	newApplication = func() *tview.Application {
		app := tview.NewApplication()
		screen := tcell.NewSimulationScreen("UTF-8")
		if err := screen.Init(); err != nil {
			t.Fatalf("screen init: %v", err)
		}
		app.SetScreen(screen)
		screen.SetSize(width, height)
		screenCh <- screen
		return app
	}

	errCh := make(chan error, 1)
	go func() { errCh <- Run(opts) }()

	screen := <-screenCh
	time.Sleep(150 * time.Millisecond) // let the refresh ticker render at least once

	// Capture content while the app is still running: Application.Stop()
	// calls screen.Fini(), which clears the SimulationScreen's buffer, so
	// reading after quitting would only ever see a blank screen.
	headerY := -1
	for y := range height {
		if strings.Contains(screenRowString(screen, y, width), headerMarker) {
			headerY = y
			break
		}
	}
	var dumpOnFail func()
	if headerY < 0 {
		var lines []string
		for y := range height {
			if row := strings.TrimRight(screenRowString(screen, y, width), " "); row != "" {
				lines = append(lines, fmt.Sprintf("row %2d: %q", y, row))
			}
		}
		dumpOnFail = func() {
			for _, l := range lines {
				t.Log(l)
			}
		}
	}

	rows := make([]string, extraRows+1)
	if headerY >= 0 {
		for i := 0; i <= extraRows; i++ {
			rows[i] = strings.TrimRight(screenRowString(screen, headerY+i, width), " ")
		}
	}

	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}

	if headerY < 0 {
		if dumpOnFail != nil {
			dumpOnFail()
		}
		t.Fatalf("could not locate header row containing %q", headerMarker)
	}

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
			// DNS's rendered width now comes from the same render func used
			// for width measurement and cell text (col.render), instead of a
			// separate header-name switch — so this is 1 char narrower than
			// the pre-TD-21② capture, whose grow-priority for DNS was
			// accidentally inherited from whatever column used to sit at
			// its array index. See the TD-21② commit message.
			name:       "DNSOnly",
			dns:        true,
			wantHeader: "║│Src IP  │Dst IP                       │DNS     │  Success│    Loss│ Loss Ratio│        RTT│        Avg│     Jitter│   Size│    MTU│   TTL│Error                                    │Last Loss       │║",
			wantData:   "║│Auto    │example.com (93.184.216.34)  │8.8.8.8 │        0│       0│       0.0%│          -│          -│          -│     56│      -│     -│                                         │-               │║",
		},
		{
			// ASN's width is now measured from the same render func used for
			// the cell text (number + operator name — country code is no
			// longer shown), instead of a separate calcColumnWidths switch
			// that only measured the bare number — so the column is now
			// correctly sized to its full content instead of truncating to
			// "AS1513...". See the TD-21② commit message.
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
		wantGroupHeader   = "║│ ▸ core  (1 hosts)│                                   │          │        │           │            │           │           │       │       │      │                                         │       │║"
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
	// header, border, ungrouped-target, border, group-header, border, grouped-target.
	rows := captureTableRows(t, opts, 200, 50, "Src IP", 6)
	if rows[0] != wantHeader {
		t.Errorf("header mismatch:\n got  %q\n want %q", rows[0], wantHeader)
	}
	if rows[2] != wantUngrouped {
		t.Errorf("ungrouped row mismatch:\n got  %q\n want %q", rows[2], wantUngrouped)
	}
	if rows[4] != wantGroupHeader {
		t.Errorf("group header row mismatch:\n got  %q\n want %q", rows[4], wantGroupHeader)
	}
	if rows[6] != wantGroupedTarget {
		t.Errorf("grouped target row mismatch:\n got  %q\n want %q", rows[6], wantGroupedTarget)
	}
}
