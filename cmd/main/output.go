package main

import (
	"fmt"
	"io"

	"github.com/nagayon-935/mping/internal/stats"
)

// allTargetsUnresponsive reports whether every target finished with zero
// replies. Mirrors the nreceived == 0 check Apple's ping.c/ping6.c use to
// decide their exit code; only consulted when --count bounds the run (see
// exitCodeNoResponse) — the always-on TUI mode has no notion of "finished".
func allTargetsUnresponsive(targets []*stats.TargetStats) bool {
	for _, t := range targets {
		if t.GetView().Recv > 0 {
			return false
		}
	}
	return true
}

// printExitSummary writes the per-target ping statistics shown after the TUI exits.
func printExitSummary(out io.Writer, targets []*stats.TargetStats) {
	fmt.Fprintln(out, "\n--- mping statistics ---")
	for _, t := range targets {
		v := t.GetView()
		lossRate := 0.0
		if v.Sent > 0 {
			lossRate = (float64(v.Loss) / float64(v.Sent)) * 100
		}
		fmt.Fprintf(out, "%s (%s): %d packets transmitted, %d received", v.Host, v.IP, v.Sent, v.Recv)
		// Mirrors ping.c's "+N duplicates" / "N packets out of wait time":
		// both are omitted when zero.
		if v.Duplicates > 0 {
			fmt.Fprintf(out, ", +%d duplicates", v.Duplicates)
		}
		fmt.Fprintf(out, ", %.1f%% packet loss", lossRate)
		if v.LateReplies > 0 {
			fmt.Fprintf(out, ", %d packets out of wait time", v.LateReplies)
		}
		fmt.Fprintln(out)
		if v.Recv > 0 {
			fmt.Fprintf(out, "rtt min/avg/max/stddev = %.3f/%.3f/%.3f/%.3f ms\n",
				float64(v.MinRTT.Microseconds())/1000.0,
				float64(v.AvgRTT.Microseconds())/1000.0,
				float64(v.MaxRTT.Microseconds())/1000.0,
				float64(v.StdDev.Microseconds())/1000.0)
		}
		fmt.Fprintln(out)
	}
}
