package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
)

// TestMainTableColumns_DynamicFlags is TestDynamicColumnIndices's
// column-struct-model successor (TD-21②): it checks which columns are
// marked dynamic (width tracks rendered content) rather than returning a
// separate index list that could drift out of sync with the column slice.
func TestMainTableColumns_DynamicFlags(t *testing.T) {
	tests := []struct {
		name       string
		dnsEnabled bool
		asnEnabled bool
		ptrEnabled bool
		want       []string // names of columns expected to be dynamic, in order
	}{
		{"neither enabled", false, false, false, []string{"Src IP", "Dst IP"}},
		{"dns only", true, false, false, []string{"Src IP", "Dst IP", "DNS"}},
		{"asn only", false, true, false, []string{"Src IP", "Dst IP", "ASN"}},
		{"ptr only", false, false, true, []string{"Src IP", "Dst IP", "PTR"}},
		{"all enabled", true, true, true, []string{"Src IP", "Dst IP", "DNS", "ASN", "PTR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols := mainTableColumns(tt.dnsEnabled, tt.asnEnabled, tt.ptrEnabled, false)
			var got []string
			for _, c := range cols {
				if c.dynamic {
					got = append(got, c.name)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("dynamic columns = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMainTableColumns_DSCP verifies the DSCP column follows ASN/PTR's
// dynamic-column pattern (absent by default, present when enabled) but is
// itself NOT a "dynamic" (width-tracks-content) column — it mirrors TTL's
// fixed-width short-text convention instead, since its values are short
// codepoint names/numbers, not variable-length text like a hostname or PTR
// record.
func TestMainTableColumns_DSCP(t *testing.T) {
	withoutDSCP := mainTableColumns(false, false, false, false)
	if idx := columnsByName(withoutDSCP, "DSCP"); idx != -1 {
		t.Fatalf("DSCP column present when dscpEnabled=false: index %d", idx)
	}

	withDSCP := mainTableColumns(false, false, false, true)
	idx := columnsByName(withDSCP, "DSCP")
	if idx == -1 {
		t.Fatal("DSCP column missing when dscpEnabled=true")
	}
	if withDSCP[idx].dynamic {
		t.Error("DSCP column should not be dynamic (fixed-width, like TTL)")
	}
}

func TestDSCPColumnRender(t *testing.T) {
	cols := mainTableColumns(false, false, false, true)
	idx := columnsByName(cols, "DSCP")
	if idx == -1 {
		t.Fatal("DSCP column missing")
	}

	tests := []struct {
		name     string
		lastDSCP int
		want     string
	}{
		// CS0 is DSCP 0, which is ambiguous with "unobserved" by design;
		// see dscpDisplayString.
		{"no observation yet, or CS0", 0, "-"},
		{"EF observed", 46 << 2, "EF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := columnRowContext{view: stats.TargetView{LastDSCP: tt.lastDSCP}}
			if got := cols[idx].render(ctx); got != tt.want {
				t.Errorf("render(LastDSCP=%d) = %q, want %q", tt.lastDSCP, got, tt.want)
			}
		})
	}
}

// columnTestView is a fully populated target view for the row-rendering tests.
func columnTestView() stats.TargetView {
	return stats.TargetView{
		Host:      "example.com",
		IP:        "1.1.1.1",
		ASN:       "AS15169",
		PTR:       "one.one.one.one",
		DNSServer: "8.8.8.8",
		Recv:      10,
		Loss:      2,
		LastRTT:   12 * time.Millisecond,
		AvgRTT:    10 * time.Millisecond,
		Jitter:    2 * time.Millisecond,
		IfaceMTU:  1500,
		LastTTL:   64,
	}
}

// TestRenderRowTexts is TestBuildFullColumns's column-struct-model successor
// (TD-21③): renderRowTexts + mainTableColumns replace buildFullColumns.
func TestRenderRowTexts(t *testing.T) {
	noPTR := columnTestView()
	noPTR.PTR = ""
	tests := []struct {
		name          string
		dns, asn, ptr bool
		view          stats.TargetView
		wantLen       int
		wantCell      map[int]string // column index -> rendered text
	}{
		{
			name: "asn only", asn: true, view: columnTestView(), wantLen: 14,
			// Dst IP includes the hostname when host != IP.
			wantCell: map[int]string{0: "10.0.0.2", 1: "example.com (1.1.1.1)", 2: "AS15169"},
		},
		{
			name: "dns and asn", dns: true, asn: true, view: columnTestView(), wantLen: 15,
			wantCell: map[int]string{1: "example.com (1.1.1.1)", 2: "8.8.8.8", 3: "AS15169"},
		},
		{
			name: "dns, asn and ptr", dns: true, asn: true, ptr: true, view: columnTestView(), wantLen: 16,
			wantCell: map[int]string{4: "one.one.one.one"},
		},
		{
			// The PTR column falls back to blank (not a placeholder) when the
			// target's PTR field is unset, e.g. the lookup failed or hasn't
			// completed yet — the plain IP is still visible via the
			// always-present Dst IP column.
			name: "ptr falls back to blank", dns: true, asn: true, ptr: true, view: noPTR, wantLen: 16,
			wantCell: map[int]string{4: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := columnRowContext{view: tt.view, sourceIPv4: "10.0.0.2", packetSize: 56, lossRate: calcLossRate(tt.view)}
			texts := renderRowTexts(mainTableColumns(tt.dns, tt.asn, tt.ptr, false), ctx)
			if len(texts) != tt.wantLen {
				t.Fatalf("texts len = %d, want %d", len(texts), tt.wantLen)
			}
			for i, want := range tt.wantCell {
				if texts[i] != want {
					t.Errorf("texts[%d] = %q, want %q", i, texts[i], want)
				}
			}
			if ctx.lossRate <= 0 {
				t.Errorf("loss rate = %v, want > 0", ctx.lossRate)
			}
		})
	}
}

// TestRenderRowCells is TestBuildFullRowCells's column-struct-model
// successor (TD-21③): renderRowCells replaces buildFullRowCells and its
// hardcoded "column index + dns/asn offset" color switch — each column now
// owns its own color logic via column.color.
func TestRenderRowCells(t *testing.T) {
	view := stats.TargetView{
		Host: "d", IP: "d", ASN: "AS123",
		Recv: 1, Loss: 2,
		LastRTT: 300 * time.Millisecond, AvgRTT: 300 * time.Millisecond, Jitter: 60 * time.Millisecond,
		IfaceMTU: 1500, LastTTL: 64,
		LastError: "err",
	}
	ctx := columnRowContext{view: view, packetSize: 56, lossRate: 90.0}

	tests := []struct {
		name          string
		dns, asn, ptr bool
	}{
		{"asn only", false, true, false},
		{"dns and asn", true, true, false},
		{"dns, asn and ptr", true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols := mainTableColumns(tt.dns, tt.asn, tt.ptr, false)
			widths := make([]int, len(cols))
			for i := range widths {
				widths[i] = 5
			}
			cells := renderRowCells(cols, renderRowTexts(cols, ctx), widths, make([]int, len(cols)), ctx, tcell.ColorWhite)
			if len(cells) != len(cols) {
				t.Fatalf("cells len = %d, want %d", len(cells), len(cols))
			}
			withThresholds(DefaultThresholds(), func() {
				// Columns that own a color override the row color; the others
				// keep it.
				wantColor := map[string]tcell.Color{
					"Error":      vividRed,
					"Loss Ratio": lossColorForRate(90),
					"RTT":        rttColorForRTT(300 * time.Millisecond),
					"Jitter":     jitterColorForJitter(60 * time.Millisecond),
					"Size":       tcell.ColorWhite,
				}
				for name, want := range wantColor {
					idx := columnsByName(cols, name)
					if idx < 0 {
						t.Fatalf("column %q missing", name)
					}
					if got := cellForeground(cells[idx]); got != want {
						t.Errorf("%s cell color = %v, want %v", name, got, want)
					}
				}
			})
		})
	}
}
