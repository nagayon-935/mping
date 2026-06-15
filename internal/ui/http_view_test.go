package ui

import (
	"strings"
	"testing"
	"time"

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
	out := renderHTTPMonitorTable(nil, 120, nil, nil, nil)
	if out == "" {
		t.Error("renderHTTPMonitorTable returned empty string for nil results")
	}
	if !strings.Contains(out, "Waiting") {
		t.Errorf("expected 'Waiting' placeholder for empty results, got:\n%s", out)
	}
}

func TestRenderHTTPMonitorTable_UpRow(t *testing.T) {
	r := makeHTTPResult("https://example.com/health", "Up", 200, 5*time.Millisecond)
	out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 160, nil, nil, nil)
	if !strings.Contains(out, "https://example.com/health") {
		t.Errorf("URL not found in output:\n%s", out)
	}
	if !strings.Contains(out, "Up") {
		t.Errorf("Status 'Up' not found in output:\n%s", out)
	}
	if !strings.Contains(out, "200") {
		t.Errorf("Code '200' not found in output:\n%s", out)
	}
}

func TestRenderHTTPMonitorTable_DownRow(t *testing.T) {
	r := makeHTTPResult("https://example.com", "Down", 503, 10*time.Millisecond)
	out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 160, nil, nil, nil)
	if !strings.Contains(out, "Down") {
		t.Errorf("Status 'Down' not found in output:\n%s", out)
	}
	if !strings.Contains(out, "503") {
		t.Errorf("Code '503' not found in output:\n%s", out)
	}
}

func TestRenderHTTPMonitorTable_ErrorRow(t *testing.T) {
	r := makeHTTPResult("https://unreachable.example.com", "Error", 0, 0)
	out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 160, nil, nil, nil)
	if !strings.Contains(out, "Error") {
		t.Errorf("Status 'Error' not found in output:\n%s", out)
	}
}

func TestRenderHTTPMonitorTable_CheckingRow(t *testing.T) {
	r := stats.NewHTTPCheckResult("https://example.com")
	out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 160, nil, nil, nil)
	if !strings.Contains(out, "Checking") {
		t.Errorf("'Checking...' status not found in output:\n%s", out)
	}
}

func TestRenderHTTPMonitorTable_MultipleRows(t *testing.T) {
	results := []*stats.HTTPCheckResult{
		makeHTTPResult("https://a.example.com", "Up", 200, 5*time.Millisecond),
		makeHTTPResult("https://b.example.com", "Down", 500, 8*time.Millisecond),
	}
	out := renderHTTPMonitorTable(results, 160, nil, nil, nil)
	if !strings.Contains(out, "https://a.example.com") {
		t.Errorf("First URL not found:\n%s", out)
	}
	if !strings.Contains(out, "https://b.example.com") {
		t.Errorf("Second URL not found:\n%s", out)
	}
}

func TestRenderHTTPMonitorTable_NarrowTerminalCompact(t *testing.T) {
	r := makeHTTPResult("https://example.com", "Up", 200, 5*time.Millisecond)
	// Narrow width should trigger compact mode (no Min/Avg/Max columns)
	outNarrow := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 60, nil, nil, nil)
	outWide := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 160, nil, nil, nil)
	// Wide output should be longer
	if len(outWide) <= len(outNarrow) {
		t.Errorf("Wide output (%d chars) should be longer than narrow (%d chars)", len(outWide), len(outNarrow))
	}
}

func TestRenderHTTPMonitorTable_HeadersPresent(t *testing.T) {
	r := makeHTTPResult("https://example.com", "Up", 200, 5*time.Millisecond)
	out := renderHTTPMonitorTable([]*stats.HTTPCheckResult{r}, 160, nil, nil, nil)
	for _, hdr := range []string{"URL", "Status", "Code", "Last", "Up", "Down"} {
		if !strings.Contains(out, hdr) {
			t.Errorf("Header %q not found in output:\n%s", hdr, out)
		}
	}
}
