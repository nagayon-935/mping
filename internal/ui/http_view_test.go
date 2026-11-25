package ui

import (
	"regexp"
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
	out := renderHTTPMonitorTable(nil, 120, nil, nil)
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
			out := renderHTTPMonitorTable(tt.results, 160, nil, nil)
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
	outNarrow := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 60, nil, nil)
	outWide := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 160, nil, nil)
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

	out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 200, lastStatuses, func(msg string) { appendErrorLog(&logs, logView, msg) })

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

func TestHTTPLatenciesUseNeutralColour(t *testing.T) {
	for _, tt := range []struct {
		width, wantCells int
	}{{60, 1}, {160, 4}} {
		r := makeHTTPResult("https://example.com", "Up", 200, 288*time.Millisecond)
		out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, tt.width, nil, nil)
		for _, cell := range strings.Split(out, "│") {
			if strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(cell, "[white]"), "[white]")) == "288.00ms" {
				tt.wantCells--
			}
		}
		if tt.wantCells != 0 || !strings.Contains(out, "[green] Up") {
			t.Errorf("width %d: expected uncoloured latency cells and a green Up status:\n%s", tt.width, out)
		}
	}
}

func TestHTTPMonitorTable_FitsURLAndAlignsNumericCells(t *testing.T) {
	const url = "http://[2001:db8::1]/health"
	r := makeHTTPResult(url, "Up", 200, 99999990*time.Microsecond)
	codeAndLatency := regexp.MustCompile(`│ +200 │ +99999\.99ms │`)

	for _, tt := range []struct {
		name    string
		width   int
		full    bool
		wantURL bool
	}{
		{"full spans pane and fits URL", 220, true, true},
		{"medium drops optional columns, URL stays whole", 100, false, true},
		{"narrow keeps the URL whole", 85, false, true},
		{"too narrow shrinks the URL below its minimum", 55, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, tt.width, nil, nil)
			plain := tview.NewTextView().SetDynamicColors(true).SetText(out).GetText(true)
			assertBoxTableWidth(t, out, tt.width)
			if visible := strings.Contains(plain, url); visible != tt.wantURL {
				t.Errorf("URL visibility = %v, want %v:\n%s", visible, tt.wantURL, plain)
			}
			if !tt.wantURL && !strings.Contains(plain, ". │ Up ") {
				t.Errorf("expected an ellipsis in the URL cell:\n%s", plain)
			}
			if !codeAndLatency.MatchString(plain) {
				t.Errorf("code and latency should be right-aligned with padding:\n%s", plain)
			}
			if strings.Contains(plain, "s ago") != tt.full {
				t.Errorf("elapsed time should appear only in the full layout:\n%s", plain)
			}
		})
	}
}
