package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
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
	wait(fmt.Sprintf("Target #%d", b.ID))
	h.press('w')
	wait(fmt.Sprintf("Save target #%d report", b.ID))
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	if r := recvWithin(t, requests, "the target save callback"); r.format != "text" || r.id != b.ID {
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
