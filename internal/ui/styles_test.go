package ui

import (
	"testing"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
)

// TestLevelColor checks the colour each level is drawn in.
func TestLevelColor(t *testing.T) {
	tests := []struct {
		level stats.Level
		want  tcell.Color
	}{
		{stats.LevelNone, tcell.ColorWhite},
		{stats.LevelPending, tcell.ColorWhite},
		{stats.LevelOK, tcell.ColorGreen},
		{stats.LevelWarn, tcell.ColorOrange},
		{stats.LevelCrit, vividRed},
	}
	for _, tt := range tests {
		if got := levelColor(tt.level); got != tt.want {
			t.Errorf("levelColor(%q) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestStatusColorTag(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"Open", "[green]"},
		{"Closed", "[red]"},
		{"Filtered", "[yellow]"},
		{"Open|Filtered", "[yellow]"},
		{"Unknown", "[white]"},
		{"", "[white]"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := statusColorTag(tt.status); got != tt.want {
				t.Fatalf("statusColorTag(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestMakeDoubleBorderDrawFunc(t *testing.T) {
	borderColor := tcell.ColorWhite
	drawFunc := makeDoubleBorderDrawFunc(" Test Title ", &borderColor)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()

	// Too small to draw a border: nothing is drawn and the inner area is empty.
	if _, _, iw, ih := drawFunc(screen, 0, 0, 1, 1); iw != -1 || ih != -1 {
		t.Fatalf("small dims: inner w=%d h=%d, want -1 -1", iw, ih)
	}

	ix, iy, iw, ih := drawFunc(screen, 0, 0, 40, 10)
	if ix != 1 || iy != 1 || iw != 38 || ih != 8 {
		t.Fatalf("normal dims: ix=%d iy=%d iw=%d ih=%d, want 1 1 38 8", ix, iy, iw, ih)
	}
	corners := []struct {
		x, y int
		want rune
	}{{0, 0, '╔'}, {39, 0, '╗'}, {0, 9, '╚'}, {39, 9, '╝'}}
	for _, c := range corners {
		r, _, style, _ := screen.GetContent(c.x, c.y)
		if fg, _, _ := style.Decompose(); r != c.want || fg != tcell.ColorWhite {
			t.Errorf("corner (%d,%d) = %q in %v, want %q in white", c.x, c.y, r, fg, c.want)
		}
	}

	// The border color is read on every draw, so changing it takes effect.
	borderColor = tcell.ColorRed
	drawFunc(screen, 0, 0, 40, 10)
	_, _, style, _ := screen.GetContent(0, 0)
	if fg, _, _ := style.Decompose(); fg != tcell.ColorRed {
		t.Errorf("border color after change = %v, want red", fg)
	}
}
