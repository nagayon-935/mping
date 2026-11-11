package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// newSimScreen returns an initialized simulation screen of the given size.
func newSimScreen(t *testing.T, width, height int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(width, height)
	return screen
}

// paintFrame draws like a tview primitive: a blank background first, then
// text over it, including a wide rune and a combining mark.
func paintFrame(screen tcell.Screen, text string, style tcell.Style) {
	screen.Clear()
	for x := range 10 {
		screen.SetContent(x, 1, ' ', nil, style)
	}
	for x, r := range text {
		screen.SetContent(x, 1, r, nil, style)
	}
	screen.SetContent(0, 2, '日', nil, style)
	screen.SetContent(4, 2, 'e', []rune{'́'}, style)
	screen.Show()
}

func TestDiffScreenShowsTheSameCellsAsDrawingDirectly(t *testing.T) {
	red := tcell.StyleDefault.Foreground(tcell.ColorRed)
	direct := newSimScreen(t, 12, 4)
	inner := newSimScreen(t, 12, 4)
	wrapped := newDiffScreen(inner)

	// The second frame is shorter, so cells of the first must be blanked.
	for _, text := range []string{"hello mping", "bye"} {
		paintFrame(direct, text, red)
		paintFrame(wrapped, text, red)

		for y := range 4 {
			for x := range 12 {
				wr, wc, ws, ww := direct.GetContent(x, y)
				gr, gc, gs, gw := inner.GetContent(x, y)
				if wr != gr || string(wc) != string(gc) || ws != gs || ww != gw {
					t.Errorf("%q cell (%d,%d): got %q%q width %d, want %q%q width %d", text, x, y, gr, gc, gw, wr, wc, ww)
				}
				fr, fc, fs, fw := wrapped.GetContent(x, y)
				if wr != fr || string(wc) != string(fc) || ws != fs || ww != fw {
					t.Errorf("%q frame cell (%d,%d): got %q%q width %d, want %q%q width %d", text, x, y, fr, fc, fw, wr, wc, ww)
				}
			}
		}
	}
}

func TestDiffScreenRedrawsEverythingAfterResize(t *testing.T) {
	inner := newSimScreen(t, 12, 4)
	wrapped := newDiffScreen(inner)
	paintFrame(wrapped, "hello", tcell.StyleDefault)

	inner.SetSize(20, 6)
	inner.Clear() // what a real terminal resize leaves behind
	paintFrame(wrapped, "hello", tcell.StyleDefault)

	if got := screenVisibleRow(inner, 1, 5); got != "hello" {
		t.Errorf("row after resize = %q, want %q", got, "hello")
	}
	wrapped.SetContent(19, 5, 'x', nil, tcell.StyleDefault)
	wrapped.Show()
	if r, _, _, _ := inner.GetContent(19, 5); r != 'x' {
		t.Errorf("cell in the grown area = %q, want 'x'", r)
	}
}

func TestDiffScreenIgnoresOutOfRangeCells(t *testing.T) {
	wrapped := newDiffScreen(newSimScreen(t, 4, 2))
	wrapped.Clear()

	wrapped.SetContent(-1, 0, 'x', nil, tcell.StyleDefault)
	wrapped.SetContent(4, 0, 'x', nil, tcell.StyleDefault)
	wrapped.SetContent(0, 2, 'x', nil, tcell.StyleDefault)
	wrapped.Show()

	if r, _, _, w := wrapped.GetContent(9, 9); r != 0 || w != 0 {
		t.Errorf("out-of-range GetContent = %q width %d, want zero values", r, w)
	}
}

func TestDiffScreenKeepsCellColorsForColorNone(t *testing.T) {
	inner := newSimScreen(t, 4, 2)
	wrapped := newDiffScreen(inner)
	wrapped.Clear()
	wrapped.SetContent(0, 0, 'a', nil, tcell.StyleDefault.Foreground(tcell.ColorRed).Background(tcell.ColorBlue))

	wrapped.SetContent(0, 0, 'b', nil, tcell.StyleDefault.Foreground(tcell.ColorNone).Background(tcell.ColorNone))
	wrapped.Show()

	_, _, style, _ := inner.GetContent(0, 0)
	if fg, bg, _ := style.Decompose(); fg != tcell.ColorRed || bg != tcell.ColorBlue {
		t.Errorf("colors = %v on %v, want red on blue", fg, bg)
	}
}
