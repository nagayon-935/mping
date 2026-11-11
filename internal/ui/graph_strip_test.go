package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nagayon-935/mping/internal/stats"
)

const ms = time.Millisecond

func TestBucketHistory(t *testing.T) {
	tests := []struct {
		name         string
		history      []time.Duration
		windowPoints int
		width        int
		want         []stripBucket
	}{
		{
			name:    "samples land right-aligned, slowest reply per column",
			history: []time.Duration{10 * ms, 30 * ms, 20 * ms, 5 * ms}, windowPoints: 8, width: 4,
			want: []stripBucket{{}, {}, {max: 30 * ms, has: true}, {max: 20 * ms, has: true}},
		},
		{
			name:    "a lost probe marks its column",
			history: []time.Duration{10 * ms, 0}, windowPoints: 2, width: 2,
			want: []stripBucket{{max: 10 * ms, has: true}, {lost: true, has: true}},
		},
		{
			name:    "only the trailing window is drawn",
			history: []time.Duration{999 * ms, 1 * ms, 2 * ms}, windowPoints: 2, width: 2,
			want: []stripBucket{{max: 1 * ms, has: true}, {max: 2 * ms, has: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bucketHistory(tt.history, tt.windowPoints, tt.width)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("bucket %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestPercentile95IgnoresLossAndSpikes(t *testing.T) {
	// Arrange: 19 replies around 15 ms, one 680 ms spike and lost probes.
	h := make([]time.Duration, 0, 25)
	for i := range 19 {
		h = append(h, time.Duration(10+i%10)*ms)
	}
	h = append(h, 680*ms, 0, 0, 0)

	// Act
	got := percentile95([][]time.Duration{h}, len(h))

	// Assert
	if got >= 680*ms || got < 15*ms {
		t.Errorf("p95 = %v, want the spike excluded and lost probes ignored", got)
	}
	if percentile95([][]time.Duration{{0, 0}}, 2) != 0 {
		t.Error("p95 of only lost probes should be 0")
	}
}

func TestStripCell(t *testing.T) {
	tests := []struct {
		name      string
		bucket    stripBucket
		wantRune  rune
		wantColor tcell.Color
	}{
		{"no samples", stripBucket{}, ' ', tcell.ColorDefault},
		{"all lost", stripBucket{lost: true, has: true}, '×', vividRed},
		{"above scale", stripBucket{max: 680 * ms, has: true}, '▲', vividRed},
		{"low reply", stripBucket{max: 1 * ms, has: true}, '▁', vividCyan},
		{"top of scale", stripBucket{max: 80 * ms, has: true}, '█', vividCyan},
		{"reply with some loss", stripBucket{max: 40 * ms, lost: true, has: true}, '▅', vividRed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, c := stripCell(tt.bucket, 80)
			if r != tt.wantRune || c != tt.wantColor {
				t.Errorf("stripCell = %q/%v, want %q/%v", r, c, tt.wantRune, tt.wantColor)
			}
		})
	}
}

func TestStripStats(t *testing.T) {
	tests := []struct {
		name    string
		history []time.Duration
		want    string
	}{
		{"all lost", []time.Duration{0, 0}, "loss 100%"},
		{"spike above scale is marked", []time.Duration{12 * ms, 15 * ms, 680 * ms}, "p95 680ms  max ▲680ms"},
		{"within scale", []time.Duration{4 * ms, 6 * ms}, "p95 6.0ms  max  6.0ms"},
		{"no samples", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripStats(tt.history, 10, 80); got != tt.want {
				t.Errorf("stripStats = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGraphViewDrawsAMiniChartPerTarget(t *testing.T) {
	// Arrange: three targets, one of them lost, in a 100-column pane.
	var targets []*stats.TargetStats
	for _, host := range []string{"1.1.1.1", "8.8.8.8", "192.0.2.1"} {
		s := stats.NewTargetStats(host)
		for range 5 {
			s.IncSent()
			if host == "192.0.2.1" {
				s.OnFailure("Timeout")
			} else {
				s.OnSuccess(15*ms, 57)
			}
		}
		targets = append(targets, s)
	}
	g := NewGraphView(targets, time.Second)
	g.mini = true
	g.SetBorder(true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 12)
	g.SetRect(0, 0, 100, 12)

	// Act
	g.Draw(screen)
	screen.Show()

	// Assert: a legend, then two cells per row, each a header and 3 bar rows.
	lines := screenLines(screen, 100, 12)
	if !strings.Contains(lines[1], "RTT 5m · p95 scale per host") {
		t.Errorf("legend line = %q", lines[1])
	}
	if !strings.Contains(lines[2], "1.1.1.1") || !strings.Contains(lines[2], "8.8.8.8") {
		t.Errorf("first cell row header = %q, want 1.1.1.1 and 8.8.8.8 side by side", lines[2])
	}
	if !strings.Contains(lines[6], "192.0.2.1") || !strings.Contains(lines[6], "loss 100%") {
		t.Errorf("lost target header = %q, want loss 100%%", lines[6])
	}
	if !strings.Contains(lines[9], "×") {
		t.Errorf("lost target bottom row = %q, want × marks", lines[9])
	}
	if got, want := g.preferredHeight(98), 1+2*miniCellRows+2; got != want {
		t.Errorf("preferredHeight = %d, want %d", got, want)
	}
}

func TestMiniLayout(t *testing.T) {
	tests := []struct {
		n, width, wantCols, wantRows int
	}{
		{0, 100, 1, 0},
		{1, 100, 1, 1},
		{3, 100, 2, 2},
		{3, 70, 1, 3},
	}
	for _, tt := range tests {
		if cols, rows := miniLayout(tt.n, tt.width); cols != tt.wantCols || rows != tt.wantRows {
			t.Errorf("miniLayout(%d, %d) = %d, %d, want %d, %d", tt.n, tt.width, cols, rows, tt.wantCols, tt.wantRows)
		}
	}
}

func TestMiniColumn(t *testing.T) {
	runes := func(col [miniPlotRows]miniCell) string {
		var b strings.Builder
		for i := miniPlotRows - 1; i >= 0; i-- {
			b.WriteRune(col[i].r)
		}
		return b.String()
	}
	tests := []struct {
		name   string
		bucket stripBucket
		want   string // top to bottom
	}{
		{"no samples", stripBucket{}, "   "},
		{"all lost", stripBucket{lost: true, has: true}, "  ×"},
		{"above scale", stripBucket{max: 680 * ms, has: true}, "▲██"},
		{"half scale", stripBucket{max: 40 * ms, has: true}, " ▄█"},
		{"tiny reply still shows", stripBucket{max: 1 * ms, has: true}, "  ▁"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runes(miniColumn(tt.bucket, 80)); got != tt.want {
				t.Errorf("miniColumn = %q, want %q", got, tt.want)
			}
		})
	}
}

// screenLines returns the simulation screen's rows as text.
func screenLines(screen tcell.SimulationScreen, width, height int) []string {
	cells, _, _ := screen.GetContents()
	lines := make([]string, height)
	for y := range height {
		var b strings.Builder
		for x := range width {
			c := cells[y*width+x]
			if len(c.Runes) == 0 {
				b.WriteRune(' ')
				continue
			}
			b.WriteRune(c.Runes[0])
		}
		lines[y] = b.String()
	}
	return lines
}
