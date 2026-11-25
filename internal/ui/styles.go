package ui

import (
	"github.com/nagayon-935/mping/internal/stats"

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

// levelColor is the TUI colour of a stats.Level: red when critical, orange
// for a warning, green when fine and white while there is nothing to judge.
func levelColor(l stats.Level) tcell.Color {
	switch l {
	case stats.LevelCrit:
		return vividRed
	case stats.LevelWarn:
		return tcell.ColorOrange
	case stats.LevelOK:
		return tcell.ColorGreen
	default:
		return tcell.ColorWhite
	}
}

// levelTags is one kind of cell's tview color tag per stats.Level; other
// covers the levels with nothing to judge yet (none, pending).
type levelTags struct {
	crit, warn, ok, other string
}

func (t levelTags) of(l stats.Level) string {
	switch l {
	case stats.LevelCrit:
		return t.crit
	case stats.LevelWarn:
		return t.warn
	case stats.LevelOK:
		return t.ok
	default:
		return t.other
	}
}

const levelStopped stats.Level = "stopped"

var targetStatusTags = levelTags{crit: "[red]", warn: "[orange]", ok: "[green]", other: "[gray]"}

func targetStatusLevel(v stats.TargetView) stats.Level {
	switch v.MeasurementState {
	case "stopped", "finished":
		return levelStopped
	}
	return stats.Level(v.Health.Status)
}

func statusSymbol(level stats.Level) string {
	switch level {
	case stats.LevelOK:
		return "○"
	case stats.LevelWarn:
		return "△"
	case stats.LevelCrit:
		return "×"
	case levelStopped:
		return "◼"
	default:
		return "–"
	}
}

func statusLabel(level stats.Level) string {
	switch level {
	case stats.LevelOK:
		return "OK"
	case stats.LevelWarn:
		return "Warning"
	case stats.LevelCrit:
		return "Critical"
	case levelStopped:
		return "Stopped"
	default:
		return "Waiting"
	}
}

// statusColorTag returns a tview color tag for the given port status.
func statusColorTag(status string) string {
	return levelTags{crit: "[red]", warn: "[yellow]", ok: "[green]", other: "[white]"}.of(stats.PortStatusLevel(status))
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
