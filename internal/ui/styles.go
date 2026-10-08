package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	errorLogMaxSize      = 1000 // maximum number of lines kept in the error log pane
	minRouteContentWidth = 20
	minPortContentWidth  = 20
)

var (
	vividRed  = tcell.NewRGBColor(255, 0, 0)
	vividCyan = tcell.NewRGBColor(0, 255, 255)
)

func lossColorForRate(lossRate float64) tcell.Color {
	th := getActiveThresholds()
	if lossRate > th.LossCrit {
		return vividRed
	}
	if lossRate > th.LossWarn {
		return tcell.ColorOrange
	}
	return tcell.ColorGreen
}

func rttColorForRTT(rtt time.Duration) tcell.Color {
	th := getActiveThresholds()
	if rtt > th.RTTCrit {
		return vividRed
	}
	if rtt > th.RTTWarn {
		return tcell.ColorOrange
	}
	if rtt > 0 {
		return tcell.ColorGreen
	}
	return tcell.ColorWhite
}

func jitterColorForJitter(jitter time.Duration) tcell.Color {
	th := getActiveThresholds()
	if jitter > th.JitterCrit {
		return vividRed
	}
	if jitter > th.JitterWarn {
		return tcell.ColorOrange
	}
	if jitter > 0 {
		return tcell.ColorGreen
	}
	return tcell.ColorWhite
}

// statusColorTag returns a tview color tag for the given port status.
func statusColorTag(status string) string {
	switch status {
	case "Open":
		return "[green]"
	case "Closed":
		return "[red]"
	case "Filtered", "Open|Filtered":
		return "[yellow]"
	default:
		return "[white]"
	}
}

// makeDoubleBorderDrawFunc creates a SetDrawFunc callback that draws a
// double-line (╔═╗║╚═╝) border with a centered title.
// borderColor is a pointer so the caller can change the color dynamically.
func makeDoubleBorderDrawFunc(title string, borderColor *tcell.Color) func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
	return func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		if width < 2 || height < 2 {
			return x + 1, y + 1, width - 2, height - 2
		}
		style := tcell.StyleDefault.Foreground(*borderColor)
		screen.SetContent(x, y, '╔', nil, style)
		for i := x + 1; i < x+width-1; i++ {
			screen.SetContent(i, y, '═', nil, style)
		}
		screen.SetContent(x+width-1, y, '╗', nil, style)
		screen.SetContent(x, y+height-1, '╚', nil, style)
		for i := x + 1; i < x+width-1; i++ {
			screen.SetContent(i, y+height-1, '═', nil, style)
		}
		screen.SetContent(x+width-1, y+height-1, '╝', nil, style)
		for i := y + 1; i < y+height-1; i++ {
			screen.SetContent(x, i, '║', nil, style)
			screen.SetContent(x+width-1, i, '║', nil, style)
		}
		tview.Print(screen, title, x+1, y, width-2, tview.AlignCenter, *borderColor)
		return x + 1, y + 1, width - 2, height - 2
	}
}
