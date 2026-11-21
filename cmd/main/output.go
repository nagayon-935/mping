package main

import (
	"fmt"
	"io"

	"github.com/nagayon-935/mping/internal/stats"
)

// printExitSummary writes the per-target ping statistics shown after the TUI exits.
// It reports whether any target received a reply for the count-limited exit code.
func printExitSummary(out io.Writer, targets []*stats.TargetStats) bool {
	anyReply := false
	fmt.Fprintln(out, "\n--- mping statistics ---")
	for _, t := range targets {
		v := t.GetViewWindow(0)
		// Recv counts unique, on-time replies. Duplicates add no receipt,
		// and late replies belong to probes already counted in Loss.
		unanswered := max(0, v.Sent-v.Recv)
		lossRate := 0.0
		if v.Sent > 0 {
			lossRate = (float64(min(unanswered, v.Sent)) / float64(v.Sent)) * 100
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
		if inFlight := unanswered - v.Loss; inFlight > 0 {
			fmt.Fprintf(out, ", %d in flight at exit counted as lost", inFlight)
		}
		fmt.Fprintln(out)
		if v.Recv > 0 {
			anyReply = true
			fmt.Fprintf(out, "rtt min/avg/max/stddev = %.3f/%.3f/%.3f/%.3f ms\n",
				float64(v.MinRTT.Microseconds())/1000.0,
				float64(v.AvgRTT.Microseconds())/1000.0,
				float64(v.MaxRTT.Microseconds())/1000.0,
				float64(v.StdDev.Microseconds())/1000.0)
		}
		fmt.Fprintln(out)
	}
	return anyReply
}
