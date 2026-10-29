package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestMetricColors checks the green/orange/red banding of each metric
// against the default thresholds (loss 20/80 %, RTT 50/200 ms, jitter
// 10/50 ms). TestColorFns_UseActiveThresholds covers custom thresholds.
func TestMetricColors(t *testing.T) {
	tests := []struct {
		name string
		got  func() tcell.Color
		want tcell.Color
	}{
		{"low loss is green", func() tcell.Color { return lossColorForRate(10) }, tcell.ColorGreen},
		{"mid loss is orange", func() tcell.Color { return lossColorForRate(50) }, tcell.ColorOrange},
		{"high loss is red", func() tcell.Color { return lossColorForRate(90) }, vividRed},
		{"zero RTT is white", func() tcell.Color { return rttColorForRTT(0) }, tcell.ColorWhite},
		{"low RTT is green", func() tcell.Color { return rttColorForRTT(10 * time.Millisecond) }, tcell.ColorGreen},
		{"mid RTT is orange", func() tcell.Color { return rttColorForRTT(100 * time.Millisecond) }, tcell.ColorOrange},
		{"zero jitter is white", func() tcell.Color { return jitterColorForJitter(0) }, tcell.ColorWhite},
		{"low jitter is green", func() tcell.Color { return jitterColorForJitter(5 * time.Millisecond) }, tcell.ColorGreen},
		{"mid jitter is orange", func() tcell.Color { return jitterColorForJitter(20 * time.Millisecond) }, tcell.ColorOrange},
	}
	withThresholds(DefaultThresholds(), func() {
		for _, tt := range tests {
			if got := tt.got(); got != tt.want {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
			}
		}
	})
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
