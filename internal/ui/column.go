package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// columnRowContext carries the per-row data column render/color funcs need,
// computed once per row rather than recomputed by every column.
type columnRowContext struct {
	view       stats.TargetView
	sourceIPv4 string
	sourceIPv6 string
	packetSize int
	lossRate   float64
}

// column describes one main-table column: its display properties, how its
// cell text and color are derived from a row, and where it sits in the
// shrink/grow priority order when the terminal is too narrow or too wide for
// every column's desired width.
//
// TD-21: replaces the five parallel headers/aligns/base/min/max slices plus
// the hardcoded-index shrinkOrder/growOrder arrays in tui_helpers.go, which
// silently pointed at the wrong column whenever the DNS/ASN columns were
// enabled (their presence shifts every later column's real index, but the
// priority arrays didn't know that).
type column struct {
	name           string
	align          int
	base, min, max int
	// dynamic marks a column whose width should track the widest rendered
	// value across all targets (Src/Dst IP, DNS, ASN), rather than staying
	// fixed at base.
	dynamic bool
	// shrinkPriority/growPriority order columns when fitting to available
	// width: lower shrinks/grows first. Values need not be contiguous.
	shrinkPriority int
	growPriority   int
	render         func(ctx columnRowContext) string
	// color returns a cell color override for the given row and its already
	// -rendered text; ok=false means "no override, keep the row's default
	// color".
	color func(ctx columnRowContext, text string) (tcell.Color, bool)
	// bold marks a column whose color override (when it fires) should also
	// set AttrBold, matching the legacy Loss Ratio column's styling.
	bold bool
}

// mainTableColumns returns the Ping Monitor table's column schema in display
// order. DNS/ASN are inserted after Dst IP only when enabled, exactly as the
// legacy parallel-slice construction did — but every column now carries its
// own shrink/grow priority, so enabling them can no longer desync width
// adjustment from the wrong column.
func mainTableColumns(dnsEnabled, asnEnabled bool) []column {
	cols := []column{
		{
			name: "Src IP", align: tview.AlignLeft, base: 6, min: 4, max: 45,
			dynamic: true, shrinkPriority: 20, growPriority: 10,
			render: func(ctx columnRowContext) string {
				return displaySourceIPForDst(ctx.view.IP, ctx.sourceIPv4, ctx.sourceIPv6)
			},
		},
		{
			name: "Dst IP", align: tview.AlignLeft, base: 6, min: 8, max: 60,
			dynamic: true, shrinkPriority: 10, growPriority: 0,
			render: func(ctx columnRowContext) string {
				v := ctx.view
				if v.Host != v.IP && !strings.Contains(v.Host, " ("+v.IP+")") {
					return fmt.Sprintf("%s (%s)", v.Host, v.IP)
				}
				return v.Host
			},
		},
	}
	if dnsEnabled {
		cols = append(cols, column{
			name: "DNS", align: tview.AlignLeft, base: 10, min: 7, max: 15,
			dynamic: true, shrinkPriority: 25, growPriority: 15,
			render: func(ctx columnRowContext) string { return ctx.view.DNSServer },
		})
	}
	if asnEnabled {
		cols = append(cols, column{
			name: "ASN", align: tview.AlignRight, base: 4, min: 4, max: 15,
			dynamic: true, shrinkPriority: 27, growPriority: 17,
			render: func(ctx columnRowContext) string {
				return stats.FormatASN(ctx.view.ASN, ctx.view.Org)
			},
		})
	}
	cols = append(cols,
		column{
			name: "Success", align: tview.AlignRight, base: 8, min: 5, max: 10,
			shrinkPriority: 80, growPriority: 40,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.view.Recv) },
		},
		column{
			name: "Loss", align: tview.AlignRight, base: 7, min: 4, max: 10,
			shrinkPriority: 90, growPriority: 90,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.view.Loss) },
		},
		column{
			name: "Loss Ratio", align: tview.AlignRight, base: 10, min: 6, max: 12,
			shrinkPriority: 70, growPriority: 80, bold: true,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%.1f%%", ctx.lossRate) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return lossColorForRate(ctx.lossRate), true
			},
		},
		column{
			name: "RTT", align: tview.AlignRight, base: 10, min: 7, max: 12,
			shrinkPriority: 60, growPriority: 50,
			render: func(ctx columnRowContext) string { return formatRTT(ctx.view.LastRTT) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return rttColorForRTT(ctx.view.LastRTT), true
			},
		},
		column{
			name: "Avg", align: tview.AlignRight, base: 10, min: 7, max: 12,
			shrinkPriority: 50, growPriority: 60,
			render: func(ctx columnRowContext) string { return formatRTT(ctx.view.AvgRTT) },
		},
		column{
			name: "Jitter", align: tview.AlignRight, base: 10, min: 7, max: 12,
			shrinkPriority: 40, growPriority: 70,
			render: func(ctx columnRowContext) string { return formatRTT(ctx.view.Jitter) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return jitterColorForJitter(ctx.view.Jitter), true
			},
		},
		column{
			name: "Size", align: tview.AlignRight, base: 6, min: 4, max: 8,
			shrinkPriority: 120, growPriority: 100,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.packetSize) },
		},
		column{
			name: "MTU", align: tview.AlignRight, base: 6, min: 4, max: 8,
			shrinkPriority: 100, growPriority: 110,
			render: func(ctx columnRowContext) string { return mtuString(ctx.view.IfaceMTU) },
		},
		column{
			name: "TTL", align: tview.AlignRight, base: 5, min: 3, max: 6,
			shrinkPriority: 110, growPriority: 120,
			render: func(ctx columnRowContext) string { return ttlString(ctx.view.LastTTL) },
		},
		column{
			name: "Error", align: tview.AlignLeft, base: 30, min: 8, max: 30,
			shrinkPriority: 0, growPriority: 20,
			render: func(ctx columnRowContext) string { return formatTableError(ctx.view.LastError) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				if text == "" {
					return 0, false
				}
				return vividRed, true
			},
		},
		column{
			name: "Last Loss", align: tview.AlignLeft, base: 15, min: 8, max: 18,
			shrinkPriority: 30, growPriority: 30,
			render: func(ctx columnRowContext) string { return formatLossAgo(ctx.view.LastLossTime) },
		},
	)
	return cols
}

// compactTableColumns returns the narrow-terminal fallback table's column
// schema (Host/Path/Stats/Error). Priorities reproduce the legacy behavior
// exactly: the old shrinkOrder/growOrder arrays were shared with the 13+
// column main table and filtered by bounds-checking, which happened to
// yield the effective order Path, Host, Stats, Error for both shrinking and
// growing this 4-column layout.
func compactTableColumns() []column {
	return []column{
		{name: "Host", align: tview.AlignLeft, min: 8, max: 40, shrinkPriority: 1, growPriority: 1},
		{name: "Path", align: tview.AlignLeft, min: 16, max: 80, shrinkPriority: 0, growPriority: 0},
		{name: "Stats", align: tview.AlignLeft, min: 18, max: 80, shrinkPriority: 2, growPriority: 2},
		{name: "Error", align: tview.AlignLeft, min: 8, shrinkPriority: 3, growPriority: 3}, // max set per-run from calcInitialTableErrorWidth
	}
}

// columnsByName finds a column by name, for the rare case a caller needs to
// patch a single column after construction (e.g. the Error column's
// data-derived max width).
func columnsByName(cols []column, name string) int {
	for i, c := range cols {
		if c.name == name {
			return i
		}
	}
	return -1
}

// renderRowTexts renders every column's cell text for one row from a single
// shared context, replacing the former buildFullColumns (TD-21③).
func renderRowTexts(cols []column, ctx columnRowContext) []string {
	texts := make([]string, len(cols))
	for i, c := range cols {
		texts[i] = c.render(ctx)
	}
	return texts
}

// renderRowCells builds formatted, colored table cells for one row from
// already-rendered text, replacing the former buildFullRowCells and its
// hardcoded "column index + dns/asn offset" color switch (TD-21③): each
// column now owns its own color logic, so it can't drift out of sync with
// where DNS/ASN happen to sit.
func renderRowCells(cols []column, texts []string, widths, aligns []int, ctx columnRowContext, rowColor tcell.Color) []*tview.TableCell {
	cells := make([]*tview.TableCell, len(cols))
	for i, c := range cols {
		cell := tview.NewTableCell(formatCellText(texts[i], widths[i], aligns[i])).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(rowColor).
			SetAlign(aligns[i])
		if c.color != nil {
			if col, ok := c.color(ctx, texts[i]); ok {
				cell.SetTextColor(col)
				if c.bold {
					cell.SetAttributes(tcell.AttrBold)
				}
			}
		}
		cells[i] = cell
	}
	return cells
}
