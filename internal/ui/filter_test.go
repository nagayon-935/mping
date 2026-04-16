package ui

import (
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func TestMatchesFilter(t *testing.T) {
	view := stats.TargetView{
		Host:    "example.com",
		IP:      "1.1.1.1",
		ASN:     "AS12345",
		Sent:    10,
		Recv:    8,
		Loss:    2, // 20% loss
		LastRTT: 150 * time.Millisecond,
		AvgRTT:  120 * time.Millisecond,
		Jitter:  15 * time.Millisecond,
	}

	tests := []struct {
		filter string
		want   bool
	}{
		{"", true},
		{"example", true},
		{"1.1.1.1", true},
		{"as12345", true},
		{"google", false},
		{"loss>10", true},
		{"loss>30", false},
		{"loss<30", true},
		{"loss<10", false},
		{"rtt>100", true},
		{"rtt>200", false},
		{"rtt<200", true},
		{"rtt<100", false},
		{"avg>100", true},
		{"avg>150", false},
		{"avg<150", true},
		{"avg<100", false},
		{"jitter>10", true},
		{"jitter>20", false},
		{"jitter<20", true},
		{"jitter<10", false},
		{"invalid>key", false},
		{"key<invalid", false},
		{"no-separator", false},
		{"com", true},
	}

	for _, tt := range tests {
		got := matchesFilter(view, tt.filter)
		if got != tt.want {
			t.Errorf("matchesFilter(%q): got %v, want %v", tt.filter, got, tt.want)
		}
	}
}
