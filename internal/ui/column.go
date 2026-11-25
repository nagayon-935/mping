package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// columnRowContext carries the per-row data column render/color funcs need,
// computed once per row rather than recomputed by every column.
type columnRowContext struct {
	view       stats.TargetView
	sources    sourceIPs
	packetSize int
	lossRate   float64
	// spark, when set, caches this row's sparkline so its plain text and
	// its colored cell share one bucket pass. Rows built without it compute
	// the sparkline on demand.
	spark *sparkMemo
}

// sparkMemo is one row's rendered sparkline at one width, filled on first use.
type sparkMemo struct {
	width         int
	plain, styled string
}

// sparkline returns the row's plain and loss-colored sparkline of points
// samples, width cells wide.
func (ctx columnRowContext) sparkline(points, width int) (plain, styled string) {
	if m := ctx.spark; m != nil && m.width == width && width > 0 {
		return m.plain, m.styled
	}
	plain, styled = sparklines(ctx.view.History, points, width)
	if ctx.spark != nil {
		*ctx.spark = sparkMemo{width: width, plain: plain, styled: styled}
	}
	return plain, styled
}

// column describes how a Ping Monitor column is laid out and rendered.
type column struct {
	name      string
	align     int
	base, min int
	// dynamic marks a column whose width should track the widest rendered
	// value across all targets (SRC/DST, DNS, ASN, PTR), rather than staying
	// fixed at base.
	dynamic bool
	// fixed uses base as the reference width even when the value changes.
	fixed  bool
	render func(ctx columnRowContext) string
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
	// styled, when set, returns the cell text with tview color tags, at
	// the same display width as render's text.
	styled func(ctx columnRowContext) string
	// fit, when set, draws the cell at exactly width cells (the column's
	// width less padding) instead of padding render's text, so the content
	// fills whatever width the layout gives the column.
	fit func(ctx columnRowContext, width int) (styled string)
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
	// rankErr hides error summaries after LAST LOSS and before packet counts.
	rankErr
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
// carries its own hide rank.
func mainTableColumns(o columnOptions) []column {
	cols := []column{
		{
			name: "SRC", align: tview.AlignLeft, base: 6,
			dynamic:  true,
			hideRank: explicitRank(o.SourceExplicit, rankSrcExplicit, rankSrc), setting: true,
			render: func(ctx columnRowContext) string {
				return ctx.sources.forDst(ctx.view.IP)
			},
		},
		{
			name: "DST", align: tview.AlignLeft, base: 6, min: 8,
			dynamic: true,
			render: func(ctx columnRowContext) string {
				v := ctx.view
				// Until the name resolves there is no address to append.
				if v.IP != "" && v.Host != v.IP && !strings.Contains(v.Host, " ("+v.IP+")") {
					return fmt.Sprintf("%s (%s)", v.Host, v.IP)
				}
				return v.Host
			},
		},
	}
	if o.DNS {
		cols = append(cols, column{
			name: "DNS", align: tview.AlignLeft, base: 10,
			dynamic:  true,
			hideRank: explicitRank(o.DNSExplicit, rankDNSExplicit, rankDNS), hideWhenEmpty: true,
			render: func(ctx columnRowContext) string { return ctx.view.DNSServer },
		})
	}
	if o.ASN {
		cols = append(cols, column{
			name: "ASN", align: tview.AlignRight, base: 4,
			dynamic: true, hideRank: rankASN,
			render: func(ctx columnRowContext) string {
				return stats.FormatASN(ctx.view.ASN, ctx.view.Org)
			},
		})
	}
	if o.PTR {
		cols = append(cols, column{
			name: "PTR", align: tview.AlignLeft, base: 12,
			dynamic: true, hideRank: rankPTR,
			render: func(ctx columnRowContext) string { return ctx.view.PTR },
		})
	}
	cols = append(cols,
		column{
			name: "RCV", align: tview.AlignRight, base: 6, fixed: true, hideRank: rankRecv,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.view.Recv) },
		},
		column{
			name: "LOST", align: tview.AlignRight, base: 6, fixed: true, hideRank: rankLost,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.view.Loss) },
		},
		column{
			name: "LOSS%", align: tview.AlignRight, base: 6, fixed: true, bold: true,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%.1f%%", ctx.lossRate) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return levelColor(ctx.view.Levels.Loss), true
			},
		},
		column{
			name: "RTT", align: tview.AlignRight, base: fixedMsWidth, fixed: true,
			render: func(ctx columnRowContext) string { return formatFixedMs(ctx.view.LastRTT) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return levelColor(ctx.view.Levels.LastRTT), true
			},
		},
		column{
			name: "AVG", align: tview.AlignRight, base: fixedMsWidth, fixed: true, hideRank: rankAvg,
			render: func(ctx columnRowContext) string { return formatFixedMs(ctx.view.AvgRTT) },
		},
		column{
			name: "JIT", align: tview.AlignRight, base: fixedMsWidth, fixed: true,
			render: func(ctx columnRowContext) string { return formatFixedMs(ctx.view.Jitter) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				return levelColor(ctx.view.Levels.Jitter), true
			},
		},
	)
	if o.SparkPoints > 0 {
		cols = append(cols, column{
			name: sparkColumnName, align: tview.AlignLeft, base: sparkWidth, fixed: true, hideRank: rankSpark,
			// The layout reserves sparkWidth; the cell then draws the
			// trend across the column's full width.
			render: func(ctx columnRowContext) string {
				plain, _ := ctx.sparkline(o.SparkPoints, sparkWidth)
				return plain
			},
			fit: func(ctx columnRowContext, width int) string {
				_, styled := ctx.sparkline(o.SparkPoints, width)
				return styled
			},
			color: func(columnRowContext, string) (tcell.Color, bool) { return vividCyan, true },
		})
	}
	cols = append(cols,
		column{
			name: "SIZE", align: tview.AlignRight, base: 6, fixed: true,
			hideRank: explicitRank(o.SizeExplicit, rankSizeExplicit, rankSize), setting: true,
			render: func(ctx columnRowContext) string { return fmt.Sprintf("%d", ctx.packetSize) },
		},
		column{
			name: mtuColumnName(o.MTU), align: tview.AlignRight, base: 6, fixed: true,
			hideRank: explicitRank(o.MTU, rankMTUExplicit, rankMTU), setting: !o.MTU,
			render: func(ctx columnRowContext) string {
				if o.MTU {
					return intOrDash(ctx.view.PathMTU)
				}
				return intOrDash(ctx.view.IfaceMTU)
			},
		},
		column{
			name: "TTL", align: tview.AlignRight, base: 3, fixed: true, hideRank: rankTTL,
			render: func(ctx columnRowContext) string { return intOrDash(ctx.view.LastTTL) },
		},
		column{
			name: "ERR", align: tview.AlignLeft, base: 20, fixed: true, hideRank: rankErr,
			render: func(ctx columnRowContext) string { return formatTableError(ctx.view.LastError) },
			color: func(ctx columnRowContext, text string) (tcell.Color, bool) {
				if text == "-" {
					return 0, false
				}
				return levelColor(stats.LevelCrit), true
			},
		},
		column{
			name: "LAST LOSS", align: tview.AlignLeft, base: 15, hideRank: rankLastLoss,
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
		text := tview.Escape(padBoxCell(texts[i], widths[i], aligns[i]))
		if c.fit != nil && widths[i] > boxCellPadding {
			text = " " + c.fit(ctx, widths[i]-boxCellPadding) + " "
		} else if c.styled != nil {
			// The styled text has the plain text's width; pad it the same
			// way, on the side its alignment leaves open.
			if spare := widths[i] - boxCellPadding - runewidth.StringWidth(texts[i]); spare >= 0 {
				if aligns[i] == tview.AlignRight {
					text = strings.Repeat(" ", spare+1) + c.styled(ctx) + " "
				} else {
					text = " " + c.styled(ctx) + strings.Repeat(" ", spare+1)
				}
			}
		}
		cell := tview.NewTableCell(text).
			SetBackgroundColor(tcell.ColorBlack).
			SetTextColor(rowColor).
			SetMaxWidth(widths[i]).
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
