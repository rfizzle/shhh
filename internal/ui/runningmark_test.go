package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// foldOrRunningFiles are the only non-test files that may write `▸`: each
// draws it for a folded group or a running row (or the state table's running,
// current and starting family), and nothing else.
// A prompt is `›`, a cursor or a current tab `❯`, a separator ` · `.
var foldOrRunningFiles = map[string]bool{
	"chat/steps.go": true, "chat/rewind.go": true, "chat/todorunrow.go": true,
	"components/stepcardopen.go": true, "components/inspectoragents.go": true,
	"components/inspectortodo.go": true, "components/inspectorplan.go": true,
	"components/inspectorprocesses.go": true, "components/meters.go": true,
	"components/turnsscreen.go": true, "components/plannedcard.go": true,
	"components/fanoutcard.go": true, "components/contextscreen.go": true,
	"components/doctorscreen.go": true, "components/state.go": true,
	"../cli/report/report.go": true,
}

// TestTheRunningMarkIsNotAPromptOrABullet fails when a file outside the list
// above writes `▸` in a string literal.
func TestTheRunningMarkIsNotAPromptOrABullet(t *testing.T) {
	fset := token.NewFileSet()
	for _, root := range screenSources {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			rel := strings.TrimPrefix(filepath.ToSlash(path), "./")
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if v, _ := strconv.Unquote(lit.Value); strings.ContainsRune(v, '▸') && !foldOrRunningFiles[rel] {
					t.Errorf("%s: %q draws ▸, which is a folded group's and a running row's mark only", fset.Position(lit.Pos()), v)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
