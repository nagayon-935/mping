package ui

import (
	"strings"
	"testing"
	"time"

	"mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestFormatTableError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "extract tail message",
			in:   "write ip 10.70.71.68->8.8.8.8: sendmsg: no route to host",
			want: "no route to host",
		},
		{
			name: "no separator keeps original",
			in:   "Timeout",
			want: "Timeout",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTableError(tt.in); got != tt.want {
				t.Fatalf("formatTableError(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCalcInitialTableErrorWidth_IncludesKnownCandidates(t *testing.T) {
	got := calcInitialTableErrorWidth(nil, "Error", 10)
	want := len("Communication Administratively Prohibited")
	if got < want {
		t.Fatalf("calcInitialTableErrorWidth: got %d, want at least %d", got, want)
	}
}

func TestProjectDurationsToGraph(t *testing.T) {
	data := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
	}

	values, has := projectDurationsToGraph(data, 30, 10)
	if len(values) != 10 || len(has) != 10 {
		t.Fatalf("projection size: got values=%d has=%d, want 10", len(values), len(has))
	}
	if has[9] != true || values[9] != data[len(data)-1] {
		t.Fatalf("latest sample should be right-aligned: has=%v value=%v", has[9], values[9])
	}
	if has[0] {
		t.Fatalf("left side should be empty when window is not yet filled: has=%v", has)
	}

	values2, has2 := projectDurationsToGraph(data, 2, 4)
	if len(values2) != 4 || len(has2) != 4 {
		t.Fatalf("projection2 size: got values=%d has=%d, want 4", len(values2), len(has2))
	}
	if !has2[0] || !has2[1] || !has2[2] || !has2[3] {
		t.Fatalf("expected continuous span in narrow window: has=%v", has2)
	}
}

func TestDisplaySourceIPForDst(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		src4     string
		src6     string
		expected string
	}{
		{
			name:     "ipv4 destination uses ipv4 source",
			dst:      "8.8.8.8",
			src4:     "10.0.0.2",
			src6:     "2001:db8::2",
			expected: "10.0.0.2",
		},
		{
			name:     "ipv6 destination with zone uses ipv6 source",
			dst:      "fe80::1%en0",
			src4:     "10.0.0.2",
			src6:     "fe80::2%en0",
			expected: "fe80::2%en0",
		},
		{
			name:     "ipv6 destination falls back to auto when no ipv6 source",
			dst:      "2001:4860:4860::8888",
			src4:     "10.0.0.2",
			src6:     "",
			expected: "Auto",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displaySourceIPForDst(tt.dst, tt.src4, tt.src6)
			if got != tt.expected {
				t.Fatalf("displaySourceIPForDst(%q): got %q, want %q", tt.dst, got, tt.expected)
			}
		})
	}
}

func TestFormatCellText(t *testing.T) {
	got := formatCellText("abcdefghijk", 8, tview.AlignLeft)
	if got != "abcde..." {
		t.Fatalf("left truncate: got %q, want %q", got, "abcde...")
	}

	got = formatCellText("42", 5, tview.AlignRight)
	if got != "   42" {
		t.Fatalf("right pad: got %q, want %q", got, "   42")
	}
}

func TestFitWidthsToAvailable(t *testing.T) {
	desired := []int{10, 20, 30}
	min := []int{5, 5, 5}
	max := []int{50, 50, 50}

	widths, ok := fitWidthsToAvailable(desired, min, max, 30)
	if !ok {
		t.Fatalf("fitWidthsToAvailable should succeed")
	}
	total := 0
	for i, w := range widths {
		total += w
		if w < min[i] || w > max[i] {
			t.Fatalf("width out of bounds at %d: %d", i, w)
		}
	}
	if total != 30 {
		t.Fatalf("total width: got %d, want %d", total, 30)
	}

	_, ok = fitWidthsToAvailable(desired, min, max, 10)
	if ok {
		t.Fatalf("fitWidthsToAvailable should fail when available < sum(min)")
	}
}

func TestGraphViewDraw_EmptyTargets(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 24)

	g := NewGraphView(nil, 1*time.Second)
	g.SetRect(0, 0, 80, 24)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Draw panicked: %v", r)
		}
	}()
	g.Draw(screen)
}

func TestGraphViewDraw_SingleTarget(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(120, 30)

	target := stats.NewTargetStats("example.com")
	target.OnSuccess(10*time.Millisecond, 64)
	target.OnSuccess(20*time.Millisecond, 64)
	target.OnSuccess(30*time.Millisecond, 64)

	g := NewGraphView([]*stats.TargetStats{target}, 200*time.Millisecond)
	g.SetRect(0, 0, 120, 30)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Draw panicked: %v", r)
		}
	}()
	g.Draw(screen)
}

func TestGraphViewDraw_HeaderText(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 10)

	target := stats.NewTargetStats("example.com")
	target.OnSuccess(10*time.Millisecond, 64)

	g := NewGraphView([]*stats.TargetStats{target}, 1*time.Second)
	g.SetRect(0, 0, 80, 10)

	g.Draw(screen)

	row := screenRowString(screen, 0, 80)
	if !strings.Contains(row, "example.com") {
		t.Fatalf("header missing hostname: %q", row)
	}
	if !strings.Contains(row, "10ms") {
		t.Fatalf("header missing RTT: %q", row)
	}
}

func screenRowString(screen tcell.Screen, y, width int) string {
	var b strings.Builder
	for x := 0; x < width; x++ {
		r, _, _, _ := screen.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}
