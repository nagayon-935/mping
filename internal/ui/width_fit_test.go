package ui

import (
	"slices"
	"testing"
)

func TestFitWidthsToAvailable(t *testing.T) {
	tests := []struct {
		name      string
		desired   []int
		minW      []int
		maxW      []int
		priority  []int
		available int
		wantFits  bool
		wantTotal int   // checked when wantFits
		wantExact []int // checked when non-nil
	}{
		{
			name:    "shrinks desired widths to the available width",
			desired: []int{10, 20, 30}, minW: []int{5, 5, 5}, maxW: []int{50, 50, 50},
			priority: []int{0, 1, 2}, available: 30, wantFits: true, wantTotal: 30,
		},
		{
			name:    "fails when available is below the sum of the minimums",
			desired: []int{10, 20, 30}, minW: []int{5, 5, 5}, maxW: []int{50, 50, 50},
			priority: []int{0, 1, 2}, available: 10, wantFits: false,
		},
		{
			name:    "grows widths into surplus space",
			desired: []int{10, 20, 30}, minW: []int{5, 5, 5}, maxW: []int{50, 50, 50},
			priority: []int{0, 1, 2}, available: 80, wantFits: true, wantTotal: 80,
		},
		{
			name:    "grows small desired widths up to the available width",
			desired: []int{3, 3, 3}, minW: []int{2, 2, 2}, maxW: []int{10, 10, 10},
			priority: []int{0, 1, 2}, available: 15, wantFits: true, wantTotal: 15,
		},
		{
			name:    "stops growing once every column is at its maximum",
			desired: []int{5}, minW: []int{1}, maxW: []int{5},
			priority: []int{0}, available: 20, wantFits: true, wantExact: []int{5},
		},
		{
			name:    "a minimum above the maximum wins",
			desired: []int{8}, minW: []int{10}, maxW: []int{5},
			priority: []int{0}, available: 10, wantFits: true, wantExact: []int{10},
		},
		{
			name:    "mismatched slice lengths do not fit",
			desired: []int{10, 20}, minW: []int{5}, maxW: []int{50, 50},
			priority: []int{0, 1}, available: 30, wantFits: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			widths, ok := fitWidthsToAvailable(tt.desired, tt.minW, tt.maxW, tt.priority, tt.priority, tt.available)
			if ok != tt.wantFits {
				t.Fatalf("fits = %v, want %v", ok, tt.wantFits)
			}
			if !ok {
				return
			}
			total := 0
			for i, w := range widths {
				total += w
				if hi := max(tt.maxW[i], tt.minW[i]); w < tt.minW[i] || w > hi {
					t.Errorf("width[%d] = %d outside [%d, %d]", i, w, tt.minW[i], hi)
				}
			}
			if tt.wantExact != nil {
				if !slices.Equal(widths, tt.wantExact) {
					t.Errorf("widths = %v, want %v", widths, tt.wantExact)
				}
				return
			}
			if total != tt.wantTotal {
				t.Errorf("total width = %d, want %d", total, tt.wantTotal)
			}
		})
	}
}
