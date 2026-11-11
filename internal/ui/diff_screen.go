package ui

import (
	"slices"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

// diffScreen keeps a frame in memory and hands the wrapped screen only the
// cells that differ from the previous frame.
//
// tview clears the screen and repaints every primitive on each draw, and most
// primitives fill their background with blanks before printing text. tcell
// marks a cell dirty as soon as its rune is replaced, even when a later write
// in the same frame restores it, so without this layer every non-blank cell
// is sent to the terminal again on every draw: tens of kilobytes of escape
// sequences per refresh with a few dozen hosts, whatever actually changed.
//
// Only the drawing calls tview and this package use (Clear, Fill, SetContent,
// SetCell, GetContent, Show, Sync) go through the frame; everything else is
// the wrapped screen's. All of them run on tview's draw goroutine, like the
// rest of the UI state.
type diffScreen struct {
	tcell.Screen
	width, height int
	frame         []screenCell // what has been drawn since the last Clear
	sent          []screenCell // what the wrapped screen holds
	inited        bool
	initErr       error
}

type screenCell struct {
	main  rune
	comb  []rune
	style tcell.Style
}

// unsentRune marks a cell the wrapped screen has not been given yet; no
// drawn cell can hold it, so the next Show always sends that cell.
const unsentRune rune = -1

var blankCell = screenCell{main: ' ', style: tcell.StyleDefault}

func newDiffScreen(screen tcell.Screen) *diffScreen {
	return &diffScreen{Screen: screen}
}

// openTerminalScreen opens and initializes the terminal behind a diffScreen.
func openTerminalScreen() (tcell.Screen, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	ds := newDiffScreen(screen)
	if err := ds.Init(); err != nil {
		return nil, err
	}
	return ds, nil
}

// Init initializes the wrapped screen once: tview's SetScreen calls Init
// again on a screen openTerminalScreen has already initialized.
func (s *diffScreen) Init() error {
	if !s.inited {
		s.inited = true
		s.initErr = s.Screen.Init()
	}
	return s.initErr
}

// fit matches the frame to the wrapped screen's size. After a resize nothing
// the wrapped screen holds is trusted, so every cell is sent again.
func (s *diffScreen) fit() {
	w, h := s.Screen.Size()
	if w == s.width && h == s.height && s.frame != nil {
		return
	}
	s.width, s.height = w, h
	s.frame = make([]screenCell, w*h)
	s.sent = make([]screenCell, w*h)
	for i := range s.frame {
		s.frame[i] = blankCell
		s.sent[i].main = unsentRune
	}
}

func (s *diffScreen) Clear() { s.Fill(' ', tcell.StyleDefault) }

func (s *diffScreen) Fill(r rune, style tcell.Style) {
	s.fit()
	for i := range s.frame {
		s.frame[i] = screenCell{main: r, style: inheritColors(style, s.frame[i].style)}
	}
}

func (s *diffScreen) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	if x < 0 || y < 0 || x >= s.width || y >= s.height {
		return
	}
	c := &s.frame[y*s.width+x]
	c.main = primary
	c.comb = nil
	if len(combining) > 0 {
		c.comb = slices.Clone(combining)
	}
	c.style = inheritColors(style, c.style)
}

func (s *diffScreen) SetCell(x, y int, style tcell.Style, ch ...rune) {
	if len(ch) == 0 {
		s.SetContent(x, y, ' ', nil, style)
		return
	}
	s.SetContent(x, y, ch[0], ch[1:], style)
}

func (s *diffScreen) GetContent(x, y int) (rune, []rune, tcell.Style, int) {
	if x < 0 || y < 0 || x >= s.width || y >= s.height {
		return 0, nil, tcell.StyleDefault, 0
	}
	c := s.frame[y*s.width+x]
	width := runewidth.RuneWidth(c.main)
	if width == 0 || c.main < ' ' {
		return ' ', c.comb, c.style, 1
	}
	return c.main, c.comb, c.style, width
}

func (s *diffScreen) Show() {
	s.flush()
	s.Screen.Show()
}

func (s *diffScreen) Sync() {
	s.flush()
	s.Screen.Sync()
}

// flush gives the wrapped screen the cells that changed since the last flush.
func (s *diffScreen) flush() {
	if w, h := s.Screen.Size(); w != s.width || h != s.height {
		// Resized mid-frame: this frame was laid out for the old size.
		// tview redraws on the resize event it is about to receive.
		return
	}
	for i := range s.frame {
		c := s.frame[i]
		if p := s.sent[i]; p.main == c.main && p.style == c.style && slices.Equal(p.comb, c.comb) {
			continue
		}
		s.Screen.SetContent(i%s.width, i/s.width, c.main, c.comb, c.style)
		s.sent[i] = c
	}
}

// inheritColors resolves tcell.ColorNone the way tcell's own cell buffer
// does: such a color keeps what the cell already has.
func inheritColors(style, prev tcell.Style) tcell.Style {
	fg, bg, _ := style.Decompose()
	if fg != tcell.ColorNone && bg != tcell.ColorNone {
		return style
	}
	prevFg, prevBg, _ := prev.Decompose()
	if fg == tcell.ColorNone {
		style = style.Foreground(prevFg)
	}
	if bg == tcell.ColorNone {
		style = style.Background(prevBg)
	}
	return style
}
