package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

func TestKeyboardHelpFitsContentAndScreen(t *testing.T) {
	lines := strings.Split(keyHelpText, "\n")
	contentWidth := 0
	for _, line := range lines {
		contentWidth = max(contentWidth, tview.TaggedStringWidth(line))
	}
	for _, tt := range []struct {
		name          string
		width, height int
		scroll        bool
	}{
		{"large screen", 180, 60, false},
		{"small screen", 30, 8, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			defer screen.Fini()
			screen.SetSize(tt.width, tt.height)
			app := tview.NewApplication()
			root := tview.NewPages()
			dialog := helpDialog{app: app, root: root}
			dialog.show()
			root.SetRect(0, 0, tt.width, tt.height)
			root.Draw(screen)

			help := app.GetFocus()
			x, y, w, h := help.GetRect()
			wantW, wantH := min(tt.width, contentWidth+4), min(tt.height, len(lines)+2)
			wantX, wantY := (tt.width-wantW)/2, (tt.height-wantH)/2
			if x != wantX || y != wantY || w != wantW || h != wantH {
				t.Errorf("help rect = (%d,%d,%d,%d), want (%d,%d,%d,%d)", x, y, w, h, wantX, wantY, wantW, wantH)
			}
			if tt.scroll {
				help.InputHandler()(tcell.NewEventKey(tcell.KeyEnd, 0, 0), nil)
				root.Draw(screen)
			}
			if got := screenText(screen); !strings.Contains(got, lines[len(lines)-1]) {
				t.Errorf("last help line missing from screen:\n%s", got)
			}
		})
	}
}

func TestHelpIncludesInputHandlerKeys(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "input_handler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	documented := make(map[string]bool)
	for line := range strings.SplitSeq(keyHelpText, "\n") {
		keys, _, _ := strings.Cut(line, "  ")
		for _, key := range strings.Split(keys, " / ") {
			documented[key] = true
		}
	}

	checked := 0
	ast.Inspect(file, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		for _, stmt := range sw.Body.List {
			for _, expr := range stmt.(*ast.CaseClause).List {
				var key string
				switch v := expr.(type) {
				case *ast.BasicLit:
					if v.Kind == token.CHAR {
						key, err = strconv.Unquote(v.Value)
						if err != nil {
							t.Fatal(err)
						}
					}
				case *ast.SelectorExpr:
					if pkg, ok := v.X.(*ast.Ident); ok && pkg.Name == "tcell" {
						key = strings.TrimPrefix(v.Sel.Name, "Key")
					}
				}
				if key != "" {
					checked++
					if !documented[key] {
						t.Errorf("help is missing key %q", key)
					}
				}
			}
		}
		return true
	})
	if checked == 0 {
		t.Fatal("no input handler keys found")
	}
}

func TestKeyboardHelpReturnsToPreviousPage(t *testing.T) {
	for _, tt := range []struct {
		name    string
		details bool
		close   func(*tuiHarness)
	}{
		{"escape in overview", false, func(h *tuiHarness) { h.key(tcell.KeyEscape) }},
		{"question mark in details", true, func(h *tuiHarness) { h.press('?') }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := startTUI(t, RunOptions{Targets: []*stats.TargetStats{stats.NewTargetStats("example.com")}}, 80, 24)
			marker := "? Help"
			if tt.details {
				h.key(tcell.KeyEnter)
				marker = "Esc: Back"
				h.wait(marker)
			}
			h.press('?')
			h.wait("Keyboard help")
			tt.close(h)
			h.waitWithout(marker, "Keyboard help")
			h.press('q')
			h.waitExit()
		})
	}
}
