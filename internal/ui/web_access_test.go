package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestWebAccessSurvivesLogRotationAndCopiesFullURL(t *testing.T) {
	url := "http://127.0.0.1:8080/#token=" + strings.Repeat("t", 64)
	// No retained log line contains the URL: discovery cannot depend on Log.
	logs := make([]string, errorLogMaxSize+10)
	for i := range logs {
		logs[i] = "A newer event"
	}
	h := startTUI(t, RunOptions{
		Targets:       []*stats.TargetStats{stats.NewTargetStats("example.com")},
		WebControlURL: url, InitialLogs: logs,
		OnAddHost: func(string) error { return nil },
		OnStop:    func() { t.Error("dialog forwarded a global stop shortcut") },
	}, 80, 24)
	frame := h.wait("b: URL / Copy")
	if strings.Contains(frame, "#token=") {
		t.Fatal("permanent header must not expose the control token")
	}
	h.press('b')
	h.wait("Web UI access")
	h.press('s')
	h.key(tcell.KeyEnter)
	h.wait("Copy requested")
	if got := string(h.screen.GetClipboardData()); got != url {
		t.Fatalf("copied URL %q, want the full URL %q", got, url)
	}
	h.key(tcell.KeyEscape)
	h.waitWithout("Ping Monitor", "Web UI access")
	// The same shortcut is available from details and restores that view.
	h.key(tcell.KeyEnter)
	h.wait("Host Details")
	h.press('b')
	h.wait("Web UI access")
	h.key(tcell.KeyEscape)
	h.waitWithout("Host Details", "Web UI access")
	h.key(tcell.KeyEscape)
	h.waitWithout("Ping Monitor", "Host Details")
	// Typing a host containing b must not open the URL dialog.
	h.press('a')
	h.press('b')
	h.waitWithout("Add host: b", "Web UI access")
	h.key(tcell.KeyEscape)
	h.quit()
}
