package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

func makeHTTPResult(url, status string, code int, rtt time.Duration) *stats.HTTPCheckResult {
	r := stats.NewHTTPCheckResult(url)
	if status != "Checking..." {
		var err error
		if status == "Error" {
			err = &fakeHTTPError{}
		}
		r.SetResult(code, rtt, err)
	}
	return r
}

type fakeHTTPError struct{}

func (e *fakeHTTPError) Error() string { return "connection refused" }

func TestRenderHTTPMonitorTable_Empty(t *testing.T) {
	out := renderHTTPMonitorTable(nil, stats.DefaultThresholds(), 120, nil, nil, nil)
	if out == "" {
		t.Error("renderHTTPMonitorTable returned empty string for nil results")
	}
	if !strings.Contains(out, "Waiting") {
		t.Errorf("expected 'Waiting' placeholder for empty results, got:\n%s", out)
	}
}

func TestRenderHTTPMonitorTable_Rows(t *testing.T) {
	ms := time.Millisecond
	tests := []struct {
		name    string
		results []*stats.HTTPCheckResult
		wants   []string
	}{
		{"up row", []*stats.HTTPCheckResult{makeHTTPResult("https://example.com/health", "Up", 200, 5*ms)},
			[]string{"https://example.com/health", "Up", "200"}},
		{"down row", []*stats.HTTPCheckResult{makeHTTPResult("https://example.com", "Down", 503, 10*ms)},
			[]string{"Down", "503"}},
		{"error row", []*stats.HTTPCheckResult{makeHTTPResult("https://unreachable.example.com", "Error", 0, 0)},
			[]string{"Error"}},
		{"checking row", []*stats.HTTPCheckResult{stats.NewHTTPCheckResult("https://example.com")},
			[]string{"Checking"}},
		{"multiple rows", []*stats.HTTPCheckResult{
			makeHTTPResult("https://a.example.com", "Up", 200, 5*ms),
			makeHTTPResult("https://b.example.com", "Down", 500, 8*ms),
		}, []string{"https://a.example.com", "https://b.example.com"}},
		{"headers", []*stats.HTTPCheckResult{makeHTTPResult("https://example.com", "Up", 200, 5*ms)},
			[]string{"URL", "Status", "Code", "Last", "Up", "Down"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderHTTPMonitorTable(tt.results, stats.DefaultThresholds(), 160, nil, nil, nil)
			for _, want := range tt.wants {
				if !strings.Contains(out, want) {
					t.Errorf("%q not found in output:\n%s", want, out)
				}
			}
		})
	}
}

func TestRenderHTTPMonitorTable_NarrowTerminalCompact(t *testing.T) {
	r := makeHTTPResult("https://example.com", "Up", 200, 5*time.Millisecond)
	// Narrow width should trigger compact mode (no Min/Avg/Max columns)
	outNarrow := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, stats.DefaultThresholds(), 60, nil, nil, nil)
	outWide := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, stats.DefaultThresholds(), 160, nil, nil, nil)
	// Wide output should be longer
	if len(outWide) <= len(outNarrow) {
		t.Errorf("Wide output (%d chars) should be longer than narrow (%d chars)", len(outWide), len(outNarrow))
	}
}

// ---- renderHTTPMonitorTable status-change log path ----

func TestRenderHTTPMonitorTable_StatusChangeLogsToWriter(t *testing.T) {
	r := makeHTTPResult("https://example.com/health", "Up", 200, 10*time.Millisecond)

	lastStatuses := map[string]string{
		"https://example.com/health": "Down", // pretend it was Down before
	}
	logView := tview.NewTextView()
	var logs []string

	out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, stats.DefaultThresholds(), 200, lastStatuses, &logs, logView)

	if out == "" {
		t.Error("expected non-empty output")
	}
	if len(logs) == 0 {
		t.Error("expected at least one log entry on status change")
	}
	if !strings.Contains(logView.GetText(false), "example.com") {
		t.Errorf("log view missing URL, got: %q", logView.GetText(false))
	}
	// After the call the status should be updated in lastStatuses.
	if lastStatuses["https://example.com/health"] != "Up" {
		t.Errorf("lastStatuses not updated, got %q", lastStatuses["https://example.com/health"])
	}
}
