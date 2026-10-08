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

func displaySourceIPForDst(dstIP, sourceIPv4, sourceIPv6 string) string {
	dst := dstIP
	if i := strings.Index(dst, "%"); i >= 0 {
		dst = dst[:i]
	}
	if ip := net.ParseIP(dst); ip != nil {
		if ip.To4() != nil {
			if sourceIPv4 != "" {
				return sourceIPv4
			}
			return "Auto"
		}
		if sourceIPv6 != "" {
			return sourceIPv6
		}
		return "Auto"
	}
	if strings.Contains(dstIP, ":") {
		if sourceIPv6 != "" {
			return sourceIPv6
		}
		return "Auto"
	}
	if sourceIPv4 != "" {
		return sourceIPv4
	}
	return "Auto"
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
	th := getActiveThresholds()
	var msgs []string
	if lossRate > th.LossCrit {
		if !state.lossRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): Loss Ratio %.1f%%[-]", now.Format("15:04:05"), view.Host, sourceIP, lossRate))
		}
		state.lossRed = true
	} else {
		state.lossRed = false
	}

	if view.LastRTT > th.RTTCrit {
		if !state.rttRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): RTT %v[-]", now.Format("15:04:05"), view.Host, sourceIP, view.LastRTT.Round(time.Microsecond)))
		}
		state.rttRed = true
	} else {
		state.rttRed = false
	}

	if view.Jitter > th.JitterCrit {
		if !state.jitterRed {
			msgs = append(msgs, fmt.Sprintf("[red][%s] %s (%s): Jitter %v[-]", now.Format("15:04:05"), view.Host, sourceIP, view.Jitter.Round(time.Microsecond)))
		}
		state.jitterRed = true
	} else {
		state.jitterRed = false
	}

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
