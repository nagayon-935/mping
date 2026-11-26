package ui

import (
	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	errorLogMaxSize      = 1000 // maximum number of lines kept in the error log pane
	minRouteContentWidth = 20
	maxPortTargetW       = 24 // widest minimum the port table's Target column asks for
)

var vividCyan = tcell.NewRGBColor(0, 255, 255)

// levelPalette holds the tcell color and tview tag name for each level.
var levelPalette = map[stats.Level]string{
	stats.LevelOK:   "green",
	stats.LevelWarn: "orange",
	stats.LevelCrit: "red",
}

func levelColorName(level stats.Level) string {
	if name, ok := levelPalette[level]; ok {
		return name
	}
	return "gray"
}

func levelColor(level stats.Level) tcell.Color {
	return tcell.GetColor(levelColorName(level))
}

func levelTag(level stats.Level) string {
	return "[" + levelColorName(level) + "]"
}

const levelStopped stats.Level = "stopped"

func rttColorTag(level stats.Level) string {
	return levelTag(level)
}

func statusColorTag(status string) string {
	return levelTag(stats.PortStatusLevel(status))
}

func httpStatusColorTag(status string) string {
	return levelTag(stats.HTTPStatusLevel(status))
}

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

// drawDoubleBorder draws a double-line (╔═╗║╚═╝) border with a centered title
// and returns the inner content rectangle.
func drawDoubleBorder(screen tcell.Screen, x, y, width, height int, title string, borderColor tcell.Color) (int, int, int, int) {
	if width < 2 || height < 2 {
		return x + 1, y + 1, width - 2, height - 2
	}
	style := tcell.StyleDefault.Foreground(borderColor)
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
	tview.Print(screen, title, x+1, y, width-2, tview.AlignCenter, borderColor)
	return x + 1, y + 1, width - 2, height - 2
}

// makeDoubleBorderDrawFunc creates a SetDrawFunc callback that draws a
// double-line (╔═╗║╚═╝) border with a centered title.
// borderColor is a pointer so the caller can change the color dynamically.
func makeDoubleBorderDrawFunc(title string, borderColor *tcell.Color) func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
	return func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		return drawDoubleBorder(screen, x, y, width, height, title, *borderColor)
	}
}
