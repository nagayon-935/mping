package ui

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

type alertFlags struct {
	lossRed   bool
	rttRed    bool
	jitterRed bool
}

// displaySourceIPForDst returns the source address shown for dstIP: the IPv4
// or IPv6 source matching the destination's family, or "Auto" when that
// source is unset. A destination that is not an IP literal (a hostname) is
// taken to be IPv6 only if it contains a colon.
func displaySourceIPForDst(dstIP, sourceIPv4, sourceIPv6 string) string {
	dst, _, _ := strings.Cut(dstIP, "%") // drop any IPv6 zone
	var isIPv6 bool
	if ip := net.ParseIP(dst); ip != nil {
		isIPv6 = ip.To4() == nil
	} else {
		isIPv6 = strings.Contains(dstIP, ":")
	}
	src := sourceIPv4
	if isIPv6 {
		src = sourceIPv6
	}
	if src == "" {
		return "Auto"
	}
	return src
}

// normalizeWriteIP substitutes the display source IP into raw-socket write
// errors that still show 0.0.0.0. This covers the auto-detect case: when no
// -S/-I flag is given, pinger.Source is empty so pinger.applyLastErrSource
// cannot fill it in, but the UI layer knows the auto-detected source IP
// (sourceIPv4/sourceIPv6) and can substitute it here at display time. The
// two functions are not redundant — see the comment on applyLastErrSource.
func normalizeWriteIP(errMsg, sourceIP string) string {
	if sourceIP == "" || sourceIP == "Auto" {
		return errMsg
	}
	if strings.Contains(errMsg, "write ip 0.0.0.0->") {
		return strings.Replace(errMsg, "write ip 0.0.0.0->", "write ip "+sourceIP+"->", 1)
	}
	return errMsg
}

func buildErrorLogMessage(view stats.TargetView, sourceIP string, errMsg string, ts time.Time) string {
	msg := normalizeWriteIP(errMsg, sourceIP)
	return fmt.Sprintf("[red][%s] %s (%s): %s[-]", ts.Format("15:04:05"), view.Host, sourceIP, msg)
}

func updateAlertState(view stats.TargetView, sourceIP string, lossRate float64, now time.Time, state alertFlags) (alertFlags, []string) {
	if view.Health.Configured {
		return state, nil
	} // backend events are delivered through ExternalLogCh
	var msgs []string
	// edge logs a message when a metric newly crosses its critical threshold
	// (red was false, over is true) and records whether it is over.
	edge := func(red *bool, over bool, detail func() string) {
		if over && !*red {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): %s[-]", now.Format("15:04:05"), view.Host, sourceIP, detail()))
		}
		*red = over
	}
	edge(&state.lossRed, view.Levels.Loss == stats.LevelCrit, func() string { return fmt.Sprintf("Loss Ratio %.1f%%", lossRate) })
	edge(&state.rttRed, view.Levels.LastRTT == stats.LevelCrit, func() string { return fmt.Sprintf("RTT %v", view.LastRTT.Round(time.Microsecond)) })
	edge(&state.jitterRed, view.Levels.Jitter == stats.LevelCrit, func() string { return fmt.Sprintf("Jitter %v", view.Jitter.Round(time.Microsecond)) })

	return state, msgs
}

func appendErrorLog(errorLogs *[]string, errorView *tview.TextView, msg string) {
	*errorLogs = append(*errorLogs, msg)
	if len(*errorLogs) > errorLogMaxSize {
		// Rebuild once on eviction instead of every call.
		*errorLogs = (*errorLogs)[1:]
		errorView.SetText(strings.Join(*errorLogs, "\n") + "\n")
		errorView.ScrollToEnd()
		return
	}
	fmt.Fprintf(errorView, "%s\n", msg)
	errorView.ScrollToEnd()
}

// Stable keys distinguish repeated hosts, DSCP variants and re-added targets.
func targetViewKey(v stats.TargetView) string {
	if v.ID == 0 {
		return v.Host
	}
	return fmt.Sprintf("%d", v.ID)
}
