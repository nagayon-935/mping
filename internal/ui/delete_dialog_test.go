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

// Skip the continuation cells of wide characters when reading visible text.
func screenVisibleRow(screen tcell.Screen, y, width int) string {
	var b strings.Builder
	for x := 0; x < width; {
		r, combining, _, cells := screen.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
		for _, c := range combining {
			b.WriteRune(c)
		}
		x += max(1, cells)
	}
	return b.String()
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
		dialog.show(target)
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
	b.DSCP = "EF"
	var confirmed uint64
	dialog := newDeleteDialog(app, root, func(id uint64, host string) { confirmed = id }, setFocus)
	dialog.show(b)
	// A second show attempt must not replace the target or reset the dialog.
	dialog.show(a)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(140, 45)
	root.SetRect(0, 0, 140, 45)
	root.Draw(screen)
	var text strings.Builder
	for y := 0; y < 45; y++ {
		text.WriteString(screenVisibleRow(screen, y, 140))
		text.WriteByte('\n')
	}
	for _, expected := range []string{"このホストを削除しますか？", fmt.Sprintf("対象: #%d", b.ID), "192.0.2.2", "EF", "キャンセル", "削除"} {
		if !strings.Contains(text.String(), expected) {
			t.Fatalf("dialog missing %q: %s", expected, text.String())
		}
	}
	b.SetIP("192.0.2.3")
	for _, k := range []tcell.Key{tcell.KeyRight, tcell.KeyEnter} {
		event := dialog.handle(tcell.NewEventKey(k, 0, tcell.ModNone))
		app.GetFocus().InputHandler()(event, setFocus)
	}
	if confirmed != b.ID || dialog.open || app.GetFocus() != table {
		t.Fatal("confirmed target changed or dialog failed to close")
	}
}
