package ui

import (
	"fmt"
	"slices"
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
// every column's desired width. Keeping the priority on the column itself
// (rather than in index-based arrays) means the optional DNS/ASN/PTR columns
// shifting later columns' positions cannot point width adjustment at the
// wrong column.
type column struct {
	name           string
	align          int
	base, min, max int
	// dynamic marks a column whose width should track the widest rendered
	// value across all targets (SRC/DST, DNS, ASN, PTR), rather than staying
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
	// set AttrBold.
	bold bool
	// hideRank orders the columns dropped when the terminal is too narrow to
	// show every column at its full content width: lower ranks are hidden
	// first, and 0 marks a core column that is never hidden.
	hideRank int
	// hideWhenEmpty drops the column whenever no row has a value for it
	// (every cell empty or "-"), however wide the terminal is.
	hideWhenEmpty bool
	// fill marks the column that takes the table's spare width; other text
	// columns grow only a little past their content.
	fill bool
	// setting marks a column that shows configuration rather than a
	// measurement (payload size, source address, interface MTU): hidden, its
	// value moves to the pane title when every row shares it.
	setting bool
}

// columnOptions says which optional columns exist and which columns the user
// asked for explicitly. A column the user asked for (-s, -S/-I, -d, -a, -r,
// -m) outlives every supplementary column when columns must be hidden.
type columnOptions struct {
	DNS            bool // some target was resolved through a DNS server
	DNSExplicit    bool // -d
	ASN            bool // -a
	PTR            bool // -r
	SourceExplicit bool // -S or -I
	SizeExplicit   bool // -s with a non-default payload size
	MTU            bool // -m: the MTU column shows the discovered path MTU
	// SparkPoints is how many trailing samples the RTT sparkline covers;
	// 0 leaves the sparkline column out.
	SparkPoints int
}

// sparkWidth is the RTT sparkline column's width in cells.
const sparkWidth = 16

// sparkColumnName heads the RTT sparkline column.
const sparkColumnName = "RTT 60s"

// Hide ranks. Supplementary columns (defaults, values derivable from other
// columns) go first; columns requested by an option go only after all of
// them, the cheapest to infer first.
const (
	rankSize = iota + 1
	rankMTU
	rankSrc
	rankDNS
	rankLastLoss
	rankLost
	rankRecv
	rankTTL
	rankAvg
	// rankSpark: the sparkline is each host's RTT trend right in its row
	// (the graph pane shows at most six hosts at once), so it outlives every
	// supplementary column.
	rankSpark
	rankSizeExplicit
	rankSrcExplicit
	rankDNSExplicit
	rankASN
	rankPTR
	rankMTUExplicit
)

// explicitRank returns explicit when the user asked for the column, else
// supplementary.
func explicitRank(asked bool, explicit, supplementary int) int {
	if asked {
		return explicit
	}
	return supplementary
}

// mainTableColumns returns the Ping Monitor table's column schema in display
// order. DNS/ASN/PTR are inserted after DST only when enabled; every column
// carries its own shrink/grow priority and hide rank, so enabling them cannot
// desync width adjustment.
func mainTableColumns(o columnOptions) []column {
	cols := []column{
		{
			name: "SRC", align: tview.AlignLeft, base: 6, min: 4, max: 45,
			dynamic: true, shrinkPriority: 20, growPriority: 10,
			hideRank: explicitRank(o.SourceExplicit, rankSrcExplicit, rankSrc), setting: true,
			render: func(ctx columnRowContext) string {
				return displaySourceIPForDst(ctx.view.IP, ctx.sourceIPv4, ctx.sourceIPv6)
			},
		},
		{
			name: "DST", align: tview.AlignLeft, base: 6, min: 8, max: 60,
			dynamic: true, shrinkPriority: 10, growPriority: 0, fill: true,
			render: func(ctx columnRowContext) string {
				v := ctx.view
				if v.Host != v.IP && !strings.Contains(v.Host, " ("+v.IP+")") {
					return fmt.Sprintf("%s (%s)", v.Host, v.IP)
				}
				return v.Host
			},
		},
	}
	if o.DNS {
		cols = append(cols, column{
			name: "DNS", align: tview.AlignLeft, base: 10, min: 7, max: 15,
			dynamic: true, shrinkPriority: 25, growPriority: 15,
			hideRank: explicitRank(o.DNSExplicit, rankDNSExplicit, rankDNS), hideWhenEmpty: true,
			render: func(ctx columnRowContext) string { return ctx.view.DNSServer },
		})
	}
	if o.ASN {
		cols = append(cols, column{
			name: "ASN", align: tview.AlignRight, base: 4, min: 4, max: 15,
			dynamic: true, shrinkPriority: 27, growPriority: 17, hideRank: rankASN,
			render: func(ctx columnRowContext) string {
				return stats.FormatASN(ctx.view.ASN, ctx.view.Org)
			},
		})
	}
	if o.PTR {
		cols = append(cols, column{
			name: "PTR", align: tview.AlignLeft, base: 12, min: 8, max: 40,
			dynamic: true, shrinkPriority: 28, growPriority: 18, hideRank: rankPTR,
			render: func(ctx columnRowContext) string { return ctx.view.PTR },
		})
	}
	cols = append(cols,
		column{
			name: "RCV", align: tview.AlignRight, base: 8, min: 3, max: 10,
			shrinkPriority: 80, growPriority: 40, hideRank: rankRecv,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.view.Recv) },
		},
		column{
			name: "LOST", align: tview.AlignRight, base: 7, min: 4, max: 10,
			shrinkPriority: 90, growPriority: 90, hideRank: rankLost,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.view.Loss) },
		},
		column{
			name: "LOSS%", align: tview.AlignRight, base: 10, min: 6, max: 12,
			shrinkPriority: 70, growPriority: 80, bold: true,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%.1f%%", ctx.lossRate) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return levelColor(ctx.view.Levels.Loss), true
			},
		},
		column{
			name: "RTT", align: tview.AlignRight, base: 10, min: 7, max: 12,
			shrinkPriority: 60, growPriority: 50,
			render: func(ctx columnRowContext) string { return formatRTT(ctx.view.LastRTT) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return levelColor(ctx.view.Levels.LastRTT), true
			},
		},
		column{
			name: "AVG", align: tview.AlignRight, base: 10, min: 7, max: 12,
			shrinkPriority: 50, growPriority: 60, hideRank: rankAvg,
			render: func(ctx columnRowContext) string { return formatRTT(ctx.view.AvgRTT) },
		},
		column{
			name: "JIT", align: tview.AlignRight, base: 10, min: 7, max: 12,
			shrinkPriority: 40, growPriority: 70,
			render: func(ctx columnRowContext) string { return formatRTT(ctx.view.Jitter) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return levelColor(ctx.view.Levels.Jitter), true
			},
		},
	)
	if o.SparkPoints > 0 {
		cols = append(cols, column{
			name: sparkColumnName, align: tview.AlignLeft, base: sparkWidth, min: sparkWidth, max: sparkWidth,
			shrinkPriority: 5, growPriority: 130, hideRank: rankSpark,
			render: func(ctx columnRowContext) string { return sparkline(ctx.view.History, o.SparkPoints, sparkWidth) },
			color:  func(columnRowContext, string) (tcell.Color, bool) { return vividCyan, true },
		})
	}
	cols = append(cols,
		column{
			name: "SIZE", align: tview.AlignRight, base: 6, min: 4, max: 8,
			shrinkPriority: 120, growPriority: 100,
			hideRank: explicitRank(o.SizeExplicit, rankSizeExplicit, rankSize), setting: true,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.packetSize) },
		},
		column{
			name: mtuColumnName(o.MTU), align: tview.AlignRight, base: 6, min: 4, max: 8,
			shrinkPriority: 100, growPriority: 110,
			hideRank: explicitRank(o.MTU, rankMTUExplicit, rankMTU), setting: !o.MTU,
			render: func(ctx columnRowContext) string {
				if o.MTU {
					return intOrDash(ctx.view.PathMTU)
				}
				return intOrDash(ctx.view.IfaceMTU)
			},
		},
		column{
			name: "TTL", align: tview.AlignRight, base: 5, min: 3, max: 6,
			shrinkPriority: 110, growPriority: 120, hideRank: rankTTL,
			render: func(ctx columnRowContext) string { return intOrDash(ctx.view.LastTTL) },
		},
		column{
			name: "ERR", align: tview.AlignLeft, base: 30, min: 8, max: 30,
			shrinkPriority: 0, growPriority: 20, hideWhenEmpty: true,
			render: func(ctx columnRowContext) string { return formatTableError(ctx.view.LastError) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				if text == "" {
					return 0, false
				}
				return vividRed, true
			},
		},
		column{
			name: "LAST LOSS", align: tview.AlignLeft, base: 15, min: 9, max: 18,
			shrinkPriority: 30, growPriority: 30, hideRank: rankLastLoss,
			render: func(ctx columnRowContext) string { return formatLossAgo(ctx.view.LastLossTime) },
		},
	)
	return cols
}

// mtuColumnName labels the MTU column: the discovered path MTU under -m,
// otherwise the outgoing interface's MTU.
func mtuColumnName(pathMTU bool) string {
	if pathMTU {
		return "PMTU"
	}
	return "MTU"
}

// columnIndex returns the index of the named column, or -1 if absent.
func columnIndex(cols []column, name string) int {
	return slices.IndexFunc(cols, func(c column) bool { return c.name == name })
}

// renderRowTexts renders every column's cell text for one row from a single
// shared context.
func renderRowTexts(cols []column, ctx columnRowContext) []string {
	texts := make([]string, len(cols))
	for i, c := range cols {
		texts[i] = c.render(ctx)
	}
	return texts
}

// renderRowCells builds formatted, colored table cells for one row from
// already-rendered text. Each column owns its own color logic, so it can't
// drift out of sync with where DNS/ASN/PTR happen to sit.
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
