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
)

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
