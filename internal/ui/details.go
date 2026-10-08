package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

type hostDetails struct {
	targetID uint64
	open     bool
	text     *tview.TextView
	graph    *GraphView
	pane     *tview.Flex
	footer   *tview.TextView
	opts     RunOptions
}

func newHostDetails(opts RunOptions) *hostDetails {
	d := &hostDetails{opts: opts, text: tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true), graph: NewGraphView(nil, opts.Interval)}
	d.text.SetBorder(true).SetTitle(" Host Details ").SetBackgroundColor(tcell.ColorBlack)
	d.graph.includePorts = true
	d.graph.SetBorder(true).SetTitle(" Target RTT ").SetBackgroundColor(tcell.ColorBlack)
	d.footer = tview.NewTextView().SetText("Esc: Back | Tab: Text/Graph | ↑↓: Scroll | d: Delete | w: Save | q: Quit").SetWrap(false)
	d.footer.SetBackgroundColor(tcell.ColorBlack)
	d.pane = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.text, 0, 3, true).AddItem(d.graph, 0, 2, false).AddItem(d.footer, 1, 0, false)
	return d
}

func (d *hostDetails) refresh(targets []*stats.TargetStats) bool {
	for _, t := range targets {
		if t.ID != d.targetID {
			continue
		}
		d.text.SetText(renderHostDetails(t, d.opts))
		d.graph.targets = []*stats.TargetStats{t}
		return true
	}
	return false
}

func renderHostDetails(t *stats.TargetStats, opts RunOptions) string {
	v := t.GetView()
	var b strings.Builder
	fmt.Fprintf(&b, "[yellow::b]%s[-::-]  Target #%d\n", tview.Escape(v.Host), v.ID)
	fmt.Fprintf(&b, "IP: %s\nSource: %s\nStarted: %s  (%s elapsed)\n", tview.Escape(v.IP), tview.Escape(displaySourceIPForDst(v.IP, opts.SourceIPv4, opts.SourceIPv6)), v.StartedAt.Format(time.RFC3339), time.Since(v.StartedAt).Round(time.Second))
	fmt.Fprintf(&b, "Ping statistics window: %s\n", v.WindowStartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Interval: %s | Timeout: %s | Payload: %d bytes | DSCP: %s\n\n", opts.Interval, opts.Timeout, opts.PacketSize, tview.Escape(v.DSCP))
	fmt.Fprintf(&b, "[yellow]Ping[-]\nSent: %d | Received: %d | Loss: %d | Cancelled: %d\nLoss ratio (completed attempts): %.1f%% | DUP: %d | Late: %d\n", v.Sent, v.Recv, v.Loss, v.Cancelled, calcLossRate(v), v.Duplicates, v.LateReplies)
	fmt.Fprintf(&b, "RTT last/min/avg/max: %s / %s / %s / %s\nJitter: %s | TTL: %s | Last loss: %s\nError: %s\n", formatRTT(v.LastRTT), formatRTT(v.MinRTT), formatRTT(v.AvgRTT), formatRTT(v.MaxRTT), formatRTT(v.Jitter), ttlString(v.LastTTL), formatLossAgo(v.LastLossTime), tview.Escape(v.LastError))
	fmt.Fprintf(&b, "ASN: %s %s | PTR: %s | Interface MTU: %d | PMTU payload: %d\n", tview.Escape(v.ASN), tview.Escape(v.Org), tview.Escape(v.PTR), v.IfaceMTU, v.PMTU)
	if v.IPChanges > 0 {
		fmt.Fprintf(&b, "[yellow]Destination IP changed %d times during this measurement.[-]\n", v.IPChanges)
	}
	for _, change := range v.IPHistory {
		fmt.Fprintf(&b, "  %s  IP %s\n", change.At.Format("15:04:05"), tview.Escape(change.IP))
	}
	fmt.Fprintln(&b, "\n[yellow]Route[-]")
	if len(v.TraceHops) == 0 {
		fmt.Fprintln(&b, "Traceroute not available.")
	} else {
		fmt.Fprintln(&b, tview.Escape(strings.Join(v.TraceHops, " → ")))
	}
	fmt.Fprintln(&b, "\n[yellow]MTR[-]")
	if len(v.MTRHops) == 0 {
		fmt.Fprintln(&b, "MTR not available.")
	}
	for _, hop := range v.MTRHops {
		fmt.Fprintf(&b, "%2d  %-20s Loss %.1f%%  Sent %d  Last %s  Avg %s\n", hop.TTL, tview.Escape(hop.IP), hop.LossPct, hop.Sent, formatRTT(hop.LastRTT), formatRTT(hop.AvgRTT))
	}
	fmt.Fprintln(&b, "\n[yellow]Ports[-]")
	if len(v.PortResults) == 0 {
		fmt.Fprintln(&b, "Port checks not enabled.")
	}
	for _, port := range v.PortResults {
		fmt.Fprintf(&b, "%d/%s  %s  RTT %s  Open %d  Closed/Filtered %d\n", port.Port, port.Protocol, tview.Escape(port.Status), formatRTT(port.RTT), port.OpenCount, port.ClosedCount)
	}
	fmt.Fprintln(&b, "\n[yellow]Target Events[-]")
	events, dropped := t.Events()
	if dropped > 0 {
		fmt.Fprintf(&b, "%d older events omitted.\n", dropped)
	}
	if len(events) == 0 {
		fmt.Fprintln(&b, "No events recorded.")
	}
	for _, event := range events {
		fmt.Fprintf(&b, "%s  %s: %s\n", event.At.Format("15:04:05"), tview.Escape(event.Kind), tview.Escape(event.Message))
	}
	return b.String()
}
