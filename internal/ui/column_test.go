package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// TestMainTableColumns_DynamicFlags checks which columns are marked dynamic (width tracks rendered content) rather than returning a
// separate index list that could drift out of sync with the column slice.
func TestMainTableColumns_DynamicFlags(t *testing.T) {
	tests := []struct {
		name       string
		dnsEnabled bool
		asnEnabled bool
		ptrEnabled bool
		want       []string // names of columns expected to be dynamic, in order
	}{
		{"neither enabled", false, false, false, []string{"SRC", "DST"}},
		{"dns only", true, false, false, []string{"SRC", "DST", "DNS"}},
		{"asn only", false, true, false, []string{"SRC", "DST", "ASN"}},
		{"ptr only", false, false, true, []string{"SRC", "DST", "PTR"}},
		{"all enabled", true, true, true, []string{"SRC", "DST", "DNS", "ASN", "PTR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols := mainTableColumns(columnOptions{DNS: tt.dnsEnabled, ASN: tt.asnEnabled, PTR: tt.ptrEnabled})
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

// TestRenderRowTexts checks the per-column cell text renderRowTexts produces
// for the schema mainTableColumns returns.
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
			name: "asn only", asn: true, view: columnTestView(), wantLen: 15,
			// Dst IP includes the hostname when host != IP.
			wantCell: map[int]string{0: "–", 1: "10.0.0.2", 2: "example.com (1.1.1.1)", 3: "AS15169"},
		},
		{
			name: "dns and asn", dns: true, asn: true, view: columnTestView(), wantLen: 16,
			wantCell: map[int]string{2: "example.com (1.1.1.1)", 3: "8.8.8.8", 4: "AS15169"},
		},
		{
			name: "dns, asn and ptr", dns: true, asn: true, ptr: true, view: columnTestView(), wantLen: 17,
			wantCell: map[int]string{5: "one.one.one.one"},
		},
		{
			// The PTR column falls back to blank (not a placeholder) when the
			// target's PTR field is unset, e.g. the lookup failed or hasn't
			// completed yet — the plain IP is still visible via the
			// always-present Dst IP column.
			name: "ptr falls back to blank", dns: true, asn: true, ptr: true, view: noPTR, wantLen: 17,
			wantCell: map[int]string{5: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := columnRowContext{view: tt.view, sources: sourceIPs{v4: "10.0.0.2"}, packetSize: 56, lossRate: calcLossRate(tt.view)}
			texts := renderRowTexts(mainTableColumns(columnOptions{DNS: tt.dns, ASN: tt.asn, PTR: tt.ptr}), ctx)
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

// TestRenderRowCells checks the cell colors renderRowCells produces; each
// column owns its own color logic via column.color.
func TestRenderRowCells(t *testing.T) {
	view := stats.TargetView{
		Host: "d", IP: "d", ASN: "AS123",
		Recv: 1, Loss: 2,
		LastRTT: 300 * time.Millisecond, AvgRTT: 300 * time.Millisecond, Jitter: 60 * time.Millisecond,
		IfaceMTU: 1500, LastTTL: 64,
		LastError: "err",
		Levels:    stats.Levels{Loss: stats.LevelCrit, LastRTT: stats.LevelCrit, Jitter: stats.LevelCrit},
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
			cols := mainTableColumns(columnOptions{DNS: tt.dns, ASN: tt.asn, PTR: tt.ptr})
			widths := make([]int, len(cols))
			for i := range widths {
				widths[i] = 5
			}
			cells := renderRowCells(cols, renderRowTexts(cols, ctx), widths, make([]int, len(cols)), ctx, tcell.ColorWhite)
			if len(cells) != len(cols) {
				t.Fatalf("cells len = %d, want %d", len(cells), len(cols))
			}
			// Columns that own a color override the row color; the others
			// keep it.
			wantColor := map[string]tcell.Color{
				"ERR":   vividRed,
				"LOSS%": vividRed,
				"RTT":   vividRed,
				"JIT":   vividRed,
				"SIZE":  tcell.ColorWhite,
			}
			for name, want := range wantColor {
				idx := columnIndex(cols, name)
				if idx < 0 {
					t.Fatalf("column %q missing", name)
				}
				if got := cellForeground(cells[idx]); got != want {
					t.Errorf("%s cell color = %v, want %v", name, got, want)
				}
			}
		})
	}
}

func TestDSTBeforeTheNameResolves(t *testing.T) {
	target := stats.NewTargetStats("lax-ca-us-ping.vultr.com")
	cols := mainTableColumns(columnOptions{})
	got := cols[columnIndex(cols, "DST")].render(columnRowContext{view: target.GetView()})
	if got != "lax-ca-us-ping.vultr.com" {
		t.Errorf("DST = %q, want the bare host name without empty parentheses", got)
	}
}

func TestFormatTableError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "extract tail message",
			in:   "write ip 10.70.71.68->8.8.8.8: sendmsg: no route to host",
			want: "no route to host",
		},
		{
			name: "no separator keeps original",
			in:   "Timeout",
			want: "Timeout",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTableError(tt.in); got != tt.want {
				t.Fatalf("formatTableError(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCalcInitialTableErrorWidth(t *testing.T) {
	failed := stats.TargetView{LastError: "This is a very long error message that exceeds normal width expectations!!"}
	tests := []struct {
		name        string
		views       []stats.TargetView
		minWidth    int
		wantAtLeast int
	}{
		{
			name: "includes the known error candidates", minWidth: 10,
			wantAtLeast: len("Communication Administratively Prohibited"),
		},
		{
			name: "includes a target's current error", views: []stats.TargetView{failed}, minWidth: 5,
			wantAtLeast: len("This is a very long error message"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calcInitialTableErrorWidth(tt.views, "Error", tt.minWidth); got < tt.wantAtLeast {
				t.Errorf("calcInitialTableErrorWidth = %d, want at least %d", got, tt.wantAtLeast)
			}
		})
	}
}

func TestRowSparklineIsComputedOncePerRow(t *testing.T) {
	ctx := columnRowContext{view: stats.TargetView{History: []time.Duration{10 * time.Millisecond, 0}}, spark: new(sparkMemo)}
	wantPlain, wantStyled := sparklines(ctx.view.History, 8, sparkWidth)

	plain, styled := ctx.sparkline(8)
	ctx.view.History = nil // a recompute would now draw an empty sparkline
	cachedPlain, cachedStyled := ctx.sparkline(8)

	if plain != wantPlain || styled != wantStyled {
		t.Fatalf("sparkline() = %q, %q, want %q, %q", plain, styled, wantPlain, wantStyled)
	}
	if cachedPlain != plain || cachedStyled != styled {
		t.Errorf("second call = %q, %q, want the row's cached %q, %q", cachedPlain, cachedStyled, plain, styled)
	}
}

// TestTableColorsFollowTheTargetsThresholds renders a row for a target whose
// health policy carries custom thresholds and checks the cells are coloured
// by those, not by the defaults (30ms RTT is fine under the defaults).
func TestTableColorsFollowTheTargetsThresholds(t *testing.T) {
	target := stats.NewTargetStats("example.com")
	target.SetIP("192.0.2.1")
	policy := stats.DefaultHealthPolicy()
	policy.Thresholds = stats.Thresholds{
		RTTWarn: 10 * time.Millisecond, RTTCrit: 20 * time.Millisecond,
		JitterWarn: 2 * time.Millisecond, JitterCrit: 4 * time.Millisecond,
		LossWarn: 5, LossCrit: 10,
	}
	target.ConfigureHealth(policy)
	target.IncSent()
	target.OnSuccess(30*time.Millisecond, 64)
	target.IncSent()
	target.OnSuccess(33*time.Millisecond, 64) // jitter 3ms/16: below warn
	for range 2 {
		target.IncSent()
		target.OnFailure("timeout") // loss 2/4 = 50%: above crit
	}

	tr := newTestTableRenderer([]*stats.TargetStats{target}, 200)
	tr.update()

	want := map[string]tcell.Color{"LOSS%": vividRed, "RTT": vividRed, "JIT": tcell.ColorGreen}
	for name, color := range want {
		idx := columnIndex(tr.cols, name)
		if idx < 0 {
			t.Fatalf("column %q missing", name)
		}
		if got := cellForeground(tr.table.GetCell(1, idx)); got != color {
			t.Errorf("%s cell color = %v, want %v", name, got, color)
		}
	}
}

func TestStatusColumnAtNarrowWidth(t *testing.T) {
	tests := []struct {
		name   string
		rtt    time.Duration
		state  string
		symbol string
		tag    string
	}{
		{"pending", 0, "running", "–", "[gray]"},
		{"ok", time.Millisecond, "running", "○", "[green]"},
		{"warn", 100 * time.Millisecond, "running", "△", "[orange]"},
		{"crit", 500 * time.Millisecond, "running", "×", "[red]"},
		{"stopped", time.Millisecond, "stopped", "◼", "[gray]"},
		{"finished", time.Millisecond, "finished", "◼", "[gray]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			if tt.rtt > 0 {
				target.IncSent()
				target.OnSuccess(tt.rtt, 64)
			}
			target.SetMeasurementState(tt.state)

			tr := newTestTableRenderer([]*stats.TargetStats{target}, 36)
			tr.update()
			if tr.activeHeaders[0] != "ST" || tr.widths[0] != 2 {
				t.Fatalf("first column = %q at width %d, want ST at width 2", tr.activeHeaders[0], tr.widths[0])
			}
			want := tt.tag + tt.symbol + "[-] "
			if got := tr.table.GetCell(1, 0).Text; got != want {
				t.Errorf("ST cell = %q, want %q", got, want)
			}
			if runewidth.StringWidth(tt.symbol) != 1 || tview.TaggedStringWidth(want) != 2 {
				t.Errorf("runewidth and tview must both allocate one cell for %q", tt.symbol)
			}
		})
	}
}
