package components

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestFullScreen_IsTheOnlyDoorToTheAltScreen: a screen that takes the whole
// terminal stands on the theme's ground because it reaches the terminal
// through FullScreen, and the only way to miss the ground is to ask for the
// alternate screen some other way. So the source is read: nothing outside
// this package's ground file sets a view's AltScreen, and a screen added
// later either goes through the door or fails here.
func TestFullScreen_IsTheOnlyDoorToTheAltScreen(t *testing.T) {
	root := filepath.Join("..", "..")
	door := filepath.Join(root, "ui", "components", "ground.go")
	fset := gotoken.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == door {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range as.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "AltScreen" {
					t.Errorf("%s asks for the alternate screen itself; a full screen takes it through FullScreen, which lays the ground", fset.Position(sel.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
