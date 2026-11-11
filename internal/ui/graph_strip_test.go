package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
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
