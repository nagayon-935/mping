package ui

import (
	"math"
	"testing"
	"time"
)

func TestNiceCeil(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{0.8, 1},
		{1.0, 1},
		{1.2, 2},
		{2.0, 2},
		{2.3, 2.5},
		{2.5, 2.5},
		{3.0, 5},
		{4.9, 5},
		{5.0, 5},
		{6.0, 10},
		{9.9, 10},
		{10.0, 10},
		{95.0, 100},
		{100.0, 100},
		{120.0, 200},
		{230.0, 250},
		{450.0, 500},
		{900.0, 1000},
		{0.0, 1},
	}
	for _, tt := range tests {
		got := niceCeil(tt.input)
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("niceCeil(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestComputeYScale(t *testing.T) {
	tests := []struct {
		name      string
		dataMax   time.Duration
		floorMs   float64
		wantMaxMs float64
		wantGrid0 int // 1/4
		wantGrid3 int // 4/4 == maxMs
	}{
		{
			name:      "below floor uses floor (100ms default)",
			dataMax:   50 * time.Millisecond,
			floorMs:   100,
			wantMaxMs: 100,
			wantGrid0: 25,
			wantGrid3: 100,
		},
		{
			name:      "120ms gets scaled up nicely",
			dataMax:   120 * time.Millisecond,
			floorMs:   100,
			wantMaxMs: 200,
			wantGrid0: 50,
			wantGrid3: 200,
		},
		{
			name:      "450ms -> 500",
			dataMax:   450 * time.Millisecond,
			floorMs:   100,
			wantMaxMs: 500,
			wantGrid0: 125,
			wantGrid3: 500,
		},
		{
			name:      "900ms -> 1000",
			dataMax:   900 * time.Millisecond,
			floorMs:   100,
			wantMaxMs: 1000,
			wantGrid0: 250,
			wantGrid3: 1000,
		},
		{
			name:      "zero data uses floor",
			dataMax:   0,
			floorMs:   100,
			wantMaxMs: 100,
			wantGrid0: 25,
			wantGrid3: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := computeYScale(tt.dataMax, tt.floorMs)
			if math.Abs(s.maxMs-tt.wantMaxMs) > 1e-6 {
				t.Errorf("maxMs: got %v, want %v", s.maxMs, tt.wantMaxMs)
			}
			if s.grid[0] != tt.wantGrid0 {
				t.Errorf("grid[0]: got %d, want %d", s.grid[0], tt.wantGrid0)
			}
			if s.grid[3] != tt.wantGrid3 {
				t.Errorf("grid[3]: got %d, want %d", s.grid[3], tt.wantGrid3)
			}
		})
	}
}

func TestComputeYScale_GridMonotonic(t *testing.T) {
	cases := []time.Duration{
		0, 1 * time.Millisecond, 50 * time.Millisecond,
		100 * time.Millisecond, 250 * time.Millisecond,
		500 * time.Millisecond, 1500 * time.Millisecond,
	}
	for _, d := range cases {
		s := computeYScale(d, 100)
		for i := 1; i < 4; i++ {
			if s.grid[i] <= s.grid[i-1] {
				t.Errorf("dataMax=%v: grid not monotonic: grid[%d]=%d <= grid[%d]=%d",
					d, i, s.grid[i], i-1, s.grid[i-1])
			}
		}
		if float64(s.grid[3]) != s.maxMs {
			t.Errorf("dataMax=%v: grid[3]=%d != maxMs=%v", d, s.grid[3], s.maxMs)
		}
	}
}
