package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestReportSimulationSavesSessionAndSelectedDuplicate(t *testing.T) {
	type request struct {
		path, format string
		id           uint64
	}
	requests := make(chan request, 2)
	release := make(chan struct{})
	defer close(release)
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{a, b}, OnSaveReport: func(path, format string, id uint64) error {
		requests <- request{path, format, id}
		if id != 0 {
			<-release
			return fmt.Errorf("test write denied")
		}
		return nil
	}}, 140, 45)
	frameHeight := 45
	// wait skips frames drawn at a different terminal size, so a frame from
	// before the resize cannot satisfy a marker after it.
	wait := func(marker string) string {
		t.Helper()
		return h.waitFrame(marker, func(f string) bool {
			return strings.Contains(f, marker) && strings.Count(f, "\n") == frameHeight
		})
	}
	wait("Ping Monitor")
	h.press('w')
	wait("Save session report")
	frame := wait("保存先 (Path) / 入力中")
	assertPathBox := func(frame string) {
		t.Helper()
		rows := strings.Split(frame, "\n")
		for y, row := range rows {
			if strings.Contains(row, "保存先 (Path) / 入力中") {
				if y+2 >= len(rows) || !strings.Contains(row, "╔") || !strings.Contains(row, "╗") ||
					!strings.Contains(rows[y+1], "mping-") || strings.Count(rows[y+1], "║") < 2 ||
					!strings.Contains(rows[y+2], "╚") || !strings.Contains(rows[y+2], "╝") {
					t.Fatalf("save path is not enclosed or is clipped:\n%s", frame)
				}
				return
			}
		}
		t.Fatalf("focused path missing:\n%s", frame)
	}
	assertPathBox(frame)
	// A smaller terminal must still show the field and its surrounding border.
	h.screen.SetSize(80, 24)
	frameHeight = 24
	h.key(tcell.KeyCtrlL)
	assertPathBox(wait("保存先 (Path) / 入力中"))
	// Tab cycles through both actions and returns to the framed input.
	h.key(tcell.KeyTab)
	wait(" 保存先 (Path) ─")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyTab)
	wait("保存先 (Path) / 入力中")
	h.key(tcell.KeyCtrlU)
	h.typeText("qfwz.json")
	wait("Format: JSON (.json)")
	h.key(tcell.KeyEnter)
	if r := recvWithin(t, requests, "the session save callback"); r.path != "qfwz.json" || r.format != "json" || r.id != 0 {
		t.Fatalf("incorrect session save: %+v", r)
	}
	wait("Saved report: qfwz.json")
	h.key(tcell.KeyDown)
	h.key(tcell.KeyEnter)
	wait(fmt.Sprintf("Target #%d", b.ID()))
	h.press('w')
	wait(fmt.Sprintf("Save target #%d report", b.ID()))
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	if r := recvWithin(t, requests, "the target save callback"); r.format != "json" || r.id != b.ID() {
		t.Fatalf("incorrect target save: %+v", r)
	}
	// The background save is deliberately blocked: navigation must still work.
	h.key(tcell.KeyEscape)
	wait("Ping Monitor")
	release <- struct{}{}
	wait("Save report: test write denied")
	h.press('w')
	wait("Save session report")
	h.key(tcell.KeyEscape)
	wait("Ping Monitor")
	h.quit()
}

func TestDeleteDialogDefaultsToCancelAndRestoresFocus(t *testing.T) {
	app := tview.NewApplication()
	setFocus := func(p tview.Primitive) { app.SetFocus(p) }
	graph := NewGraphView(nil, time.Second)
	graph.scrollRow = 7
	root := tview.NewPages().AddPage("main", graph, true, true)
	app.SetFocus(graph)
	confirmed := 0
	dialog := newDeleteDialog(app, root, func(uint64, string) { confirmed++ }, setFocus)
	target := stats.NewTargetStats("example")
	key := func(k tcell.Key, r rune) {
		t.Helper()
		event := dialog.handle(tcell.NewEventKey(k, r, tcell.ModNone))
		if event != nil {
			app.GetFocus().InputHandler()(event, setFocus)
		}
	}
	for _, cancel := range []tcell.Key{tcell.KeyEnter, tcell.KeyEscape} {
		dialog.show(target, false)
		button, ok := app.GetFocus().(*tview.Button)
		if !ok || button.GetLabel() != "キャンセル" {
			t.Fatal("cancel is not selected initially")
		}
		key(tcell.KeyRune, 'd')
		key(tcell.KeyRune, 'd')
		key(tcell.KeyRune, 'q')
		if !dialog.open || confirmed != 0 {
			t.Fatal("global shortcut escaped confirmation")
		}
		key(cancel, 0)
		if dialog.open || confirmed != 0 || app.GetFocus() != graph || graph.scrollRow != 7 || root.HasPage("deleteConfirm") {
			t.Fatal("cancellation changed the original view")
		}
	}
}

func TestDeleteDialogShowsAndConfirmsFrozenTarget(t *testing.T) {
	app := tview.NewApplication()
	setFocus := func(p tview.Primitive) { app.SetFocus(p) }
	table := tview.NewTable().SetOffset(5, 1)
	root := tview.NewPages().AddPage("main", table, true, true)
	app.SetFocus(table)
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	b.SetIP("192.0.2.2")
	var confirmed uint64
	dialog := newDeleteDialog(app, root, func(id uint64, host string) { confirmed = id }, setFocus)
	dialog.show(b, false)
	// A second show attempt must not replace the target or reset the dialog.
	dialog.show(a, false)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(140, 45)
	root.SetRect(0, 0, 140, 45)
	root.Draw(screen)
	text := screenText(screen)
	for _, expected := range []string{"このホストを削除しますか？", fmt.Sprintf("対象: #%d", b.ID()), "192.0.2.2", "キャンセル", "削除"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("dialog missing %q: %s", expected, text)
		}
	}
	b.SetIP("192.0.2.3")
	for _, k := range []tcell.Key{tcell.KeyRight, tcell.KeyEnter} {
		event := dialog.handle(tcell.NewEventKey(k, 0, tcell.ModNone))
		app.GetFocus().InputHandler()(event, setFocus)
	}
	if confirmed != b.ID() || dialog.open || app.GetFocus() != table {
		t.Fatal("confirmed target changed or dialog failed to close")
	}
}

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
